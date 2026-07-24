package redclaw

// TenantContext 租户上下文
type TenantContext struct {
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
}

// ExtractTenantContext 从 JWT Claims 中提取租户上下文
func ExtractTenantContext(claims map[string]interface{}) *TenantContext {
	ctx := &TenantContext{
		TenantID: "default",
		UserID:   "",
		Role:     "user",
	}

	if claims == nil {
		return ctx
	}

	if sub, ok := claims["sub"].(string); ok {
		ctx.UserID = sub
	}
	if tid, ok := claims["tenant_id"].(string); ok && tid != "" {
		ctx.TenantID = tid
	}
	if role, ok := claims["role"].(string); ok {
		ctx.Role = role
	}

	return ctx
}

// AttachTenantHeaders 生成需附加到 RedClaw 请求的 Header
func AttachTenantHeaders(ctx *TenantContext) map[string]string {
	return map[string]string{
		"X-Tenant-ID": ctx.TenantID,
		"X-User-ID":   ctx.UserID,
	}
}