package connectmac

import (
	"strings"
	"time"
)

var beijingDisplayLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func formatBeijingDisplayTime(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return value
	}
	return parsed.In(beijingDisplayLocation).Format("2006-01-02 15:04:05") + "（北京时间）"
}
