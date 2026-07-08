package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/payrail/go/internal/config"
)

type PayPal struct {
	clientID  string
	secret    string
	webhookID string
	baseURL   string
	http      *http.Client
	mu        sync.Mutex
	token     string
	tokenExp  time.Time
}

func NewPayPal(clientID, secret, webhookID, baseURL string, cfg config.GatewayConfig) *PayPal {
	if baseURL == "" {
		baseURL = "https://api-m.paypal.com" // live
		if strings.ToLower(cfg.Env) == "development" || strings.ToLower(cfg.Env) == "dev" {
			baseURL = "https://api-m.sandbox.paypal.com" // sandbox
		}
	}
	return &PayPal{
		clientID:  clientID,
		secret:    secret,
		webhookID: webhookID,
		baseURL:   baseURL,
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *PayPal) Name() string {
	return "PAYPAL"
}

func (p *PayPal) accessToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.token != "" && time.Now().Before(p.tokenExp.Add(-60*2*time.Second)) {
		return p.token, nil // cached
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(p.clientID, p.secret)

	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("paypal token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("paypal token: status %d", resp.StatusCode)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"` // seconds
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	p.token = out.AccessToken
	p.tokenExp = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)

	return out.AccessToken, nil
}

func (p *PayPal) CreateOrder(ctx context.Context, req CreateOrderRequest) (CreateOrderResult, error) {

	token, err := p.accessToken(ctx)
	if err != nil {
		return CreateOrderResult{}, err
	}

	body, _ := json.Marshal(map[string]any{
		"intent": "CAPTURE",
		"purchase_units": []map[string]any{{
			"reference_id": req.OrderID,
			"amount": map[string]any{
				"currency_code": req.Currency,
				"value":         minorToDecimal(req.AmountMinor, req.Currency),
			},
		}},
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v2/checkout/orders", bytes.NewReader(body))
	if err != nil {
		return CreateOrderResult{}, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("PayPal-Request-Id", req.OrderID) // idempotency

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return CreateOrderResult{}, fmt.Errorf("paypal create order: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return CreateOrderResult{}, fmt.Errorf("paypal create order: status %d", resp.StatusCode)
	}

	var out struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CreateOrderResult{}, err
	}

	return CreateOrderResult{
		GatewayOrderID: out.ID,
		ClientParams: map[string]any{
			"orderId":  out.ID,
			"clientId": p.clientID,
		},
	}, nil
}

func (p *PayPal) VerifyWebhook(ctx context.Context, payload []byte, headers http.Header) error {

	token, err := p.accessToken(ctx)
	if err != nil {
		return err
	}

	event := json.RawMessage(payload)

	body, _ := json.Marshal(map[string]any{
		"auth_algo":         headers.Get("PAYPAL-AUTH-ALGO"),
		"cert_url":          headers.Get("PAYPAL-CERT-URL"),
		"transmission_id":   headers.Get("PAYPAL-TRANSMISSION-ID"),
		"transmission_sig":  headers.Get("PAYPAL-TRANSMISSION-SIG"),
		"transmission_time": headers.Get("PAYPAL-TRANSMISSION-TIME"),
		"webhook_id":        p.webhookID,
		"webhook_event":     event,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/notifications/verify-webhook-signature", bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("paypal verify: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return ErrInvalidSignature
	}

	var out struct {
		VerificationStatus string `json:"verification_status"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}

	if out.VerificationStatus != "SUCCESS" {
		return ErrInvalidSignature
	}

	return nil
}

func (p *PayPal) FetchPayment(ctx context.Context, gatewayOrderID string) (FetchPaymentResult, error) {

	token, err := p.accessToken(ctx)
	if err != nil {
		return FetchPaymentResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v2/checkout/orders/"+gatewayOrderID, nil)
	if err != nil {
		return FetchPaymentResult{}, err
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return FetchPaymentResult{}, fmt.Errorf("paypal fetch order: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return FetchPaymentResult{}, fmt.Errorf("paypal fetch order: status %d", resp.StatusCode)
	}

	var out struct {
		Status        string `json:"status"` // CREATED|APPROVED|COMPLETED|VOIDED
		PurchaseUnits []struct {
			Payments struct {
				Captures []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Amount struct {
						Value        string `json:"value"`
						CurrencyCode string `json:"currency_code"`
					} `json:"amount"`
				} `json:"captures"`
			} `json:"payments"`
		} `json:"purchase_units"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return FetchPaymentResult{}, fmt.Errorf("paypal decode: %w", err)
	}
	res := FetchPaymentResult{Status: "PENDING"}

	switch out.Status {
	case "COMPLETED":
		res.Status = "CAPTURED"
	case "VOIDED":
		res.Status = "EXPIRED"
	}

	var totalAmount int64
	for _, pu := range out.PurchaseUnits {
		for _, cap := range pu.Payments.Captures {
			if cap.Status == "COMPLETED" {
				totalAmount += decimalStringToMinorPP(cap.Amount.Value)
			}
			if res.Currency == "" {
				res.Currency = cap.Amount.CurrencyCode
			}
			res.GatewayPaymentID = cap.ID // keep latest capture id
		}
	}

	res.AmountMinor = totalAmount
	return res, nil
}

func (p *PayPal) FetchRefund(ctx context.Context, gatewayRefundID, _ string) (FetchRefundResult, error) {

	token, err := p.accessToken(ctx)
	if err != nil {
		return FetchRefundResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v2/payments/refunds/"+gatewayRefundID, nil)
	if err != nil {
		return FetchRefundResult{}, err
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.http.Do(httpReq)
	if err != nil {
		return FetchRefundResult{}, fmt.Errorf("paypal fetch refund: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return FetchRefundResult{}, fmt.Errorf("paypal fetch refund: status %d", resp.StatusCode)
	}

	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"` // COMPLETED|PENDING|CANCELLED|FAILED
		Amount struct {
			Value string `json:"value"`
		} `json:"amount"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return FetchRefundResult{}, fmt.Errorf("paypal decode: %w", err)
	}

	res := FetchRefundResult{
		GatewayRefundID: out.ID,
		AmountMinor:     decimalStringToMinorPP(out.Amount.Value),
		Status:          "PENDING",
	}

	switch out.Status {
	case "COMPLETED":
		res.Status = "PROCESSED"
	case "CANCELLED", "FAILED":
		res.Status = "FAILED"
	}
	return res, nil
}

// 100.50 -> 10050
func decimalStringToMinorPP(v string) int64 {
	whole, frac, _ := strings.Cut(v, ".")
	if len(frac) > 2 {
		return 0
	}
	frac = (frac + "00")[:2]
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
