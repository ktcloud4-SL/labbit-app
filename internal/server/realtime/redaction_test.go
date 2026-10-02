package realtime_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

// Session token, attach token, Connector credential은 실수로 출력되어도 원문이 나오지 않는다.
func TestSecretTypesAreRedactedEverywhere(t *testing.T) {
	const secret = "raw-secret-value-0123456789"
	type holder struct {
		Session    realtime.SessionToken
		Attach     realtime.AttachToken
		Credential realtime.ConnectorCredential
	}
	values := map[string]any{
		"SessionToken":        realtime.SessionToken(secret),
		"AttachToken":         realtime.AttachToken(secret),
		"ConnectorCredential": realtime.ConnectorCredential(secret),
		"holder":              holder{realtime.SessionToken(secret), realtime.AttachToken(secret), realtime.ConnectorCredential(secret)},
		"pointer to holder":   &holder{realtime.SessionToken(secret), realtime.AttachToken(secret), realtime.ConnectorCredential(secret)},
		"slice":               []realtime.AttachToken{realtime.AttachToken(secret)},
		"map":                 map[string]realtime.AttachToken{"k": realtime.AttachToken(secret)},
	}

	for name, value := range values {
		outputs := map[string]string{
			"%v":     fmt.Sprintf("%v", value),
			"%+v":    fmt.Sprintf("%+v", value),
			"%#v":    fmt.Sprintf("%#v", value),
			"%s":     fmt.Sprintf("%s", value),
			"%q":     fmt.Sprintf("%q", value),
			"%x":     fmt.Sprintf("%x", value),
			"Sprint": fmt.Sprint(value),
		}
		if data, err := json.Marshal(value); err == nil {
			outputs["json"] = string(data)
		}
		var logs bytes.Buffer
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("event", "value", value, slog.Any("any", value))
		outputs["slog json"] = logs.String()
		logs.Reset()
		slog.New(slog.NewTextHandler(&logs, nil)).Info("event", "value", value)
		outputs["slog text"] = logs.String()

		for format, out := range outputs {
			if strings.Contains(out, secret) {
				t.Errorf("%s(%s)가 원문을 포함함: %s", name, format, out)
			}
			// %x는 hex로 바꿔도 원문의 byte가 드러나므로 함께 확인한다.
			if strings.Contains(out, fmt.Sprintf("%x", secret)) {
				t.Errorf("%s(%s)가 원문의 hex를 포함함: %s", name, format, out)
			}
		}
	}

	// 원문이 필요한 경계에서는 명시적 변환으로만 꺼낸다.
	if string(realtime.AttachToken(secret)) != secret {
		t.Fatal("명시적 변환은 원문을 돌려줘야 한다")
	}
}
