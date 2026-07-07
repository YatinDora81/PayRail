package gatewaypb

func GatewayName(g Gateway) string {
	switch g {
	case Gateway_GATEWAY_RAZORPAY:
		return "RAZORPAY"
	case Gateway_GATEWAY_STRIPE:
		return "STRIPE"
	case Gateway_GATEWAY_PAYPAL:
		return "PAYPAL"
	case Gateway_GATEWAY_CASHFREE:
		return "CASHFREE"
	default:
		return ""
	}
}

func GatewayFromName(name string) Gateway {
	switch name {
	case "RAZORPAY":
		return Gateway_GATEWAY_RAZORPAY
	case "STRIPE":
		return Gateway_GATEWAY_STRIPE
	case "PAYPAL":
		return Gateway_GATEWAY_PAYPAL
	case "CASHFREE":
		return Gateway_GATEWAY_CASHFREE
	default:
		return Gateway_GATEWAY_UNSPECIFIED
	}
}

func CurrencyName(c Currency) string {
	switch c {
	case Currency_CURRENCY_INR:
		return "INR"
	case Currency_CURRENCY_USD:
		return "USD"
	case Currency_CURRENCY_EUR:
		return "EUR"
	case Currency_CURRENCY_GBP:
		return "GBP"
	case Currency_CURRENCY_AED:
		return "AED"
	case Currency_CURRENCY_SGD:
		return "SGD"
	default:
		return ""
	}
}

func CurrencyFromName(name string) Currency {
	switch name {
	case "INR":
		return Currency_CURRENCY_INR
	case "USD":
		return Currency_CURRENCY_USD
	case "EUR":
		return Currency_CURRENCY_EUR
	case "GBP":
		return Currency_CURRENCY_GBP
	case "AED":
		return Currency_CURRENCY_AED
	case "SGD":
		return Currency_CURRENCY_SGD
	default:
		return Currency_CURRENCY_UNSPECIFIED
	}
}

func PaymentStatusFromName(name string) PaymentStatus {
	switch name {
	case "CAPTURED":
		return PaymentStatus_PAYMENT_STATUS_CAPTURED
	case "FAILED":
		return PaymentStatus_PAYMENT_STATUS_FAILED
	case "EXPIRED":
		return PaymentStatus_PAYMENT_STATUS_EXPIRED
	case "PENDING":
		return PaymentStatus_PAYMENT_STATUS_PENDING
	default:
		return PaymentStatus_PAYMENT_STATUS_UNSPECIFIED
	}
}

func RefundStatusFromName(name string) RefundStatus {
	switch name {
	case "PROCESSED":
		return RefundStatus_REFUND_STATUS_PROCESSED
	case "FAILED":
		return RefundStatus_REFUND_STATUS_FAILED
	case "PENDING":
		return RefundStatus_REFUND_STATUS_PENDING
	default:
		return RefundStatus_REFUND_STATUS_UNSPECIFIED
	}
}
