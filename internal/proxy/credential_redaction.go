package proxy

import "regexp"

var sensitiveJSONValue = regexp.MustCompile(`(?i)("(?:token_v2|full_cookie|authorization|cookie|x-api-key|api_key|admin_password|password)"\s*:\s*)"(?:\\.|[^"\\])*"`)
var sensitivePlainValue = regexp.MustCompile(`(?im)((?:token_v2|full_cookie|authorization|cookie|x-api-key|api_key|api key|admin password)\s*[:=]\s*)[^\r\n]+`)
var sensitiveBearerValue = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)

func redactSensitiveText(text string) string {
	text = sensitiveJSONValue.ReplaceAllString(text, `${1}"[REDACTED]"`)
	text = sensitivePlainValue.ReplaceAllString(text, `${1}[REDACTED]`)
	return sensitiveBearerValue.ReplaceAllString(text, "Bearer [REDACTED]")
}
