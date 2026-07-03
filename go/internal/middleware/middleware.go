package middleware

import "context"

type AllowFunc func(ctx context.Context, key string) bool
