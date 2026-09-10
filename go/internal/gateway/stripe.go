package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Stripe struct {
	secretKey      string
	publishableKey string
	webhookSecret  string
	baseURL        string
	tolerance      time.Duration
	http           *http.Client
}

func NewStripe(secretKey, publishableKey, webhookSecret, baseURL string) *Stripe {
	if baseURL == "" {
		baseURL = "https://api.stripe.com"
	}
	return &Stripe{
		secretKey:      secretKey,
		publishableKey: publishableKey,
		webhookSecret:  webhookSecret,
		baseURL:        baseURL,
		tolerance:      5 * time.Minute,
		http:           &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *Stripe) Name() string {
	return "STRIPE"
}

const stripeAPIVersion = "2026-07-29.dahlia"

func (s *Stripe) auth(r *http.Request) {
	r.SetBasicAuth(s.secretKey, "")
	r.Header.Set("Stripe-Version", stripeAPIVersion)
}

func (s *Stripe) CreateOrder(ctx context.Context, req CreateOrderRequest) (CreateOrderResult, error) {

	form := url.Values{}
	form.Set("amount", strconv.FormatInt(req.AmountMinor, 10))
	form.Set("currency", strings.ToLower(req.Currency))
	form.Set("automatic_payment_methods[enabled]", "true")
	form.Set("metadata[orderId]", req.OrderID)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v1/payment_intents", strings.NewReader(form.Encode()))
	if err != nil {
		return CreateOrderResult{}, err
	}

	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.auth(httpReq)
	httpReq.Header.Set("Idempotency-Key", req.OrderID)

	resp, err := s.http.Do(httpReq)
	if err != nil {
		return CreateOrderResult{}, fmt.Errorf("stripe create intent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return CreateOrderResult{}, fmt.Errorf("stripe create intent: status %d", resp.StatusCode)
	}

	var out struct {
		ID           string `json:"id"`
		ClientSecret string `json:"client_secret"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CreateOrderResult{}, fmt.Errorf("stripe decode: %w", err)
	}

	return CreateOrderResult{
		GatewayOrderID: out.ID,
		ClientParams: map[string]any{
			"clientSecret":   out.ClientSecret,
			"publishableKey": s.publishableKey,
		},
	}, nil
}

func (s *Stripe) VerifyWebhook(_ context.Context, payload []byte, headers http.Header) error {
	sigHeader := headers.Get("Stripe-Signature")
	if sigHeader == "" {
		return ErrInvalidSignature
	}

	var timestamp string
	var v1s []string

	for _, part := range strings.Split(sigHeader, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch strings.TrimSpace(kv[0]) {
		case "t":
			timestamp = kv[1]
		case "v1":
			v1s = append(v1s, kv[1])
		}
	}

	if timestamp == "" || len(v1s) == 0 {
		return ErrInvalidSignature
	}

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrInvalidSignature
	}
	if time.Since(time.Unix(ts, 0)) > s.tolerance {
		return ErrInvalidSignature // stale / replayed
	}

	signed := timestamp + "." + string(payload)
	want := hmacSHA256Hex([]byte(s.webhookSecret), []byte(signed))
	for _, got := range v1s {
		if secureEqual(got, want) {
			return nil
		}
	}

	return ErrInvalidSignature
}

func (s *Stripe) FetchPayment(ctx context.Context, gatewayOrderID string) (FetchPaymentResult, error) {

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/v1/payment_intents/"+gatewayOrderID, nil)
	if err != nil {
		return FetchPaymentResult{}, err
	}
	s.auth(httpReq)
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return FetchPaymentResult{}, fmt.Errorf("stripe fetch intent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return FetchPaymentResult{}, fmt.Errorf("stripe fetch intent: status %d", resp.StatusCode)
	}

	var out struct {
		Status         string `json:"status"`
		AmountReceived int64  `json:"amount_received"`
		Currency       string `json:"currency"`
		LatestCharge   string `json:"latest_charge"` // identify payment attempt capture
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return FetchPaymentResult{}, fmt.Errorf("stripe decode: %w", err)
	}

	res := FetchPaymentResult{
		GatewayPaymentID: gatewayOrderID,
		AmountMinor:      out.AmountReceived,
		Currency:         strings.ToUpper(out.Currency),
		Status:           "PENDING",
	}

	switch out.Status {
	case "succeeded":
		res.Status = "CAPTURED"
	case "canceled":
		res.Status = "EXPIRED"
	}
	return res, nil
}

func (s *Stripe) FetchRefund(ctx context.Context, gatewayRefundID, _ string) (FetchRefundResult, error) {

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/v1/refunds/"+gatewayRefundID, nil)
	if err != nil {
		return FetchRefundResult{}, err
	}
	s.auth(httpReq)
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return FetchRefundResult{}, fmt.Errorf("stripe fetch refund: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return FetchRefundResult{}, fmt.Errorf("stripe fetch refund: status %d", resp.StatusCode)
	}

	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"` // pending|succeeded|failed|canceled
		Amount int64  `json:"amount"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return FetchRefundResult{}, fmt.Errorf("stripe decode: %w", err)
	}
	res := FetchRefundResult{GatewayRefundID: out.ID, AmountMinor: out.Amount, Status: "PENDING"}
	switch out.Status {
	case "succeeded":
		res.Status = "PROCESSED"
	case "failed", "canceled":
		res.Status = "FAILED"
	}

	return res, nil
}

func (s *Stripe) CreateRefund(ctx context.Context, req CreateRefundRequest) (CreateRefundResult, error) {

	form := url.Values{}

	form.Set("charge", req.GatewayPaymentID)
	form.Set("amount", strconv.FormatInt(req.AmountMinor, 10))
	form.Set("metadata[idempotencyKey]", req.IdempotencyKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v1/refunds", strings.NewReader(form.Encode()))
	if err != nil {
		return CreateRefundResult{}, err
	}

	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.auth(httpReq)

	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return CreateRefundResult{}, fmt.Errorf("stripe create refund: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 4 {
		return CreateRefundResult{}, fmt.Errorf("stripe create refund: status %d: %w", resp.StatusCode, ErrRefundRejected)
	}
	if resp.StatusCode/100 != 2 {
		return CreateRefundResult{}, fmt.Errorf("stripe create refund: status %d", resp.StatusCode)
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"` // pending | succeeded | failed | canceled
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CreateRefundResult{}, fmt.Errorf("stripe decode: %w", err)
	}

	res := CreateRefundResult{GatewayRefundID: out.ID, Status: "PENDING"}

	switch out.Status {
	case "succeeded":
		res.Status = "PROCESSED"
	case "failed", "canceled":
		res.Status = "FAILED"
	}

	return res, nil
}

func (s *Stripe) FindOrderByReference(ctx context.Context, merchantReference string) (OrderLookupResult, error) {
	q := url.Values{}
	q.Set("query", "metadata['orderId']:'"+strings.ReplaceAll(merchantReference, "'", "")+"'")
	q.Set("limit", "1")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/v1/payment_intents/search?"+q.Encode(), nil)
	if err != nil {
		return OrderLookupResult{}, err
	}

	s.auth(httpReq)

	resp, err := s.http.Do(httpReq)
	if err != nil {
		return OrderLookupResult{}, fmt.Errorf("stripe search intent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return OrderLookupResult{}, fmt.Errorf("stripe search intent: status %d", resp.StatusCode)
	}

	var out struct {
		Data []struct {
			ID             string `json:"id"`
			Status         string `json:"status"`
			AmountReceived int64  `json:"amount_received"`
			Currency       string `json:"currency"`
			LatestCharge   string `json:"latest_charge"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return OrderLookupResult{}, fmt.Errorf("stripe decode: %w", err)
	}

	if len(out.Data) == 0 {
		return OrderLookupResult{}, nil
	}

	pi := out.Data[0]

	res := OrderLookupResult{Found: true, GatewayOrderID: pi.ID, Status: "PENDING", GatewayPaymentID: pi.LatestCharge,
		AmountMinor: pi.AmountReceived, Currency: strings.ToUpper(pi.Currency)}

	switch pi.Status {
	case "succeeded":
		res.Status = "CAPTURED"
	case "canceled":
		res.Status = "EXPIRED"
	}
	
	return res, nil
}
