package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Razorpay struct {
	keyID         string
	keySecret     string
	webhookSecret string
	baseURL       string
	http          *http.Client
}

func NewRazorpay(keyID, keySecret, webhookSecret, baseURL string) *Razorpay {
	if baseURL == "" {
		baseURL = "https://api.razorpay.com"
	}
	return &Razorpay{
		keyID:         keyID,
		keySecret:     keySecret,
		webhookSecret: webhookSecret,
		baseURL:       baseURL,
		http:          &http.Client{Timeout: 10 * time.Second},
	}
}

func (r *Razorpay) Name() string {
	return "Razorpay"
}

func (r *Razorpay) CreateOrder(ctx context.Context, req CreateOrderRequest) (CreateOrderResult, error) {

	body, _ := json.Marshal(map[string]any{
		"amount":   req.AmountMinor,
		"currency": req.Currency,
		"receipt":  req.OrderID,
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/v1/orders", bytes.NewReader(body))
	if err != nil {
		return CreateOrderResult{}, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.SetBasicAuth(r.keyID, r.keySecret)

	resp, err := r.http.Do(httpReq)
	if err != nil {
		return CreateOrderResult{}, fmt.Errorf("razorpay create order: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return CreateOrderResult{}, fmt.Errorf("razorpay create order: status %d", resp.StatusCode)
	}

	var out struct {
		ID string `json:"id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CreateOrderResult{}, fmt.Errorf("razorpay decode: %w", err)
	}

	return CreateOrderResult{
		GatewayOrderID: out.ID,
		ClientParams: map[string]any{
			"key":      r.keyID,
			"orderId":  out.ID,
			"amount":   req.AmountMinor,
			"currency": req.Currency,
		},
	}, nil
}

func (r *Razorpay) VerifyWebhook(_ context.Context, payload []byte, headers http.Header) error {
	got := headers.Get("X-Razorpay-Signature")
	if got == "" {
		return ErrInvalidSignature
	}
	want := hmacSHA256Hex([]byte(r.webhookSecret), payload)
	if !secureEqual(got, want) {
		return ErrInvalidSignature
	}
	return nil
}

func (r *Razorpay) FetchPayment(ctx context.Context, gatewayOrderID string) (FetchPaymentResult, error) {

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/v1/orders/"+gatewayOrderID+"/payments", nil)
	if err != nil {
		return FetchPaymentResult{}, err
	}

	httpReq.SetBasicAuth(r.keyID, r.keySecret)
	resp, err := r.http.Do(httpReq)
	if err != nil {
		return FetchPaymentResult{}, fmt.Errorf("razorpay fetch payment: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return FetchPaymentResult{}, fmt.Errorf("razorpay fetch payment: status %d", resp.StatusCode)
	}

	var out struct {
		Items []struct {
			ID       string `json:"id"`
			Status   string `json:"status"` // created|authorized|captured|refunded|failed
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return FetchPaymentResult{}, fmt.Errorf("razorpay decode: %w", err)
	}

	res := FetchPaymentResult{Status: "PENDING"}
	for _, p := range out.Items {
		res.GatewayPaymentID, res.AmountMinor, res.Currency = p.ID, p.Amount, p.Currency
		switch p.Status {
		case "captured", "refunded":
			res.Status = "CAPTURED"
			return res, nil
		case "failed":
			res.Status = "FAILED"
		}
	}

	if len(out.Items) == 0 {
		res.Status = "EXPIRED"
	}

	return res, nil
}

func (r *Razorpay) FetchRefund(ctx context.Context, gatewayRefundID, _ string) (FetchRefundResult, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/v1/refunds/"+gatewayRefundID, nil)
	if err != nil {
		return FetchRefundResult{}, err
	}

	httpReq.SetBasicAuth(r.keyID, r.keySecret)
	resp, err := r.http.Do(httpReq)
	if err != nil {
		return FetchRefundResult{}, fmt.Errorf("razorpay fetch refund: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return FetchRefundResult{}, fmt.Errorf("razorpay fetch refund: status %d", resp.StatusCode)
	}

	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"` // pending|processed|failed
		Amount int64  `json:"amount"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return FetchRefundResult{}, fmt.Errorf("razorpay decode: %w", err)
	}

	res := FetchRefundResult{GatewayRefundID: out.ID, AmountMinor: out.Amount, Status: "PENDING"}
	switch out.Status {
	case "processed":
		res.Status = "PROCESSED"
	case "failed":
		res.Status = "FAILED"
	}

	return res, nil
}

func (r *Razorpay) CreateRefund(ctx context.Context, req CreateRefundRequest) (CreateRefundResult, error) {
	body, _ := json.Marshal(map[string]any{
		"amount":  req.AmountMinor,
		"speed":   "normal",
		"receipt": req.IdempotencyKey,
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/v1/payments/"+req.GatewayPaymentID+"/refund", bytes.NewReader(body))
	if err != nil {
		return CreateRefundResult{}, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.SetBasicAuth(r.keyID, r.keySecret)
	resp, err := r.http.Do(httpReq)
	if err != nil {
		return CreateRefundResult{}, fmt.Errorf("razorpay create refund: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 4 {
		return CreateRefundResult{}, fmt.Errorf("razorpay create refund: status %d: %w", resp.StatusCode, ErrRefundRejected)
	}
	if resp.StatusCode/100 != 2 {
		return CreateRefundResult{}, fmt.Errorf("razorpay create refund: status %d", resp.StatusCode)
	}
	var out struct {
		ID     string `json:"id"`
		Status string `json:"status"` // pending | processed | failed
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CreateRefundResult{}, fmt.Errorf("razorpay decode: %w", err)
	}

	res := CreateRefundResult{GatewayRefundID: out.ID, Status: "PENDING"}
	switch out.Status {
	case "processed":
		res.Status = "PROCESSED"
	case "failed":
		res.Status = "FAILED"
	}
	return res, nil

}

func (r *Razorpay) FindOrderByReference(ctx context.Context, merchantReference string) (OrderLookupResult, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/v1/orders?count=1&receipt="+merchantReference, nil)
	if err != nil {
		return OrderLookupResult{}, err
	}

	httpReq.SetBasicAuth(r.keyID, r.keySecret)
	
	resp, err := r.http.Do(httpReq)
	if err != nil {
		return OrderLookupResult{}, fmt.Errorf("razorpay find order: %w", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return OrderLookupResult{}, fmt.Errorf("razorpay find order: status %d", resp.StatusCode)
	}
	var out struct {
		Items []struct {
			ID       string `json:"id"`
			Status   string `json:"status"` // created | attempted | paid
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return OrderLookupResult{}, fmt.Errorf("razorpay decode: %w", err)
	}
	if len(out.Items) == 0 {
		return OrderLookupResult{}, nil
	}
	o := out.Items[0]
	res := OrderLookupResult{Found: true, GatewayOrderID: o.ID, Status: "PENDING", AmountMinor: o.Amount, Currency: o.Currency}
	if o.Status == "paid" {
		res.Status = "CAPTURED" // reconciler fetchpayment passes 
	}
	return res, nil
}
