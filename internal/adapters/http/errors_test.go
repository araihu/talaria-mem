package httpadapter

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

func TestContractSafeErrorEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	canary := "secret-canary"
	writeError(recorder, domain.NewError(domain.CodeSecretRefusal, "content refused", false))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", recorder.Code)
	}
	if body := recorder.Body.String(); body == "" || contains(body, canary) {
		t.Fatalf("unsafe body=%q", body)
	}
}

func TestClassifyErrorNeverEchoesBackend(t *testing.T) {
	record := classifyError(errors.New("backend password=secret"))
	if record.Status != http.StatusInternalServerError || record.Message != "internal error" || contains(record.Message, "secret") {
		t.Fatalf("classification=%+v", record)
	}
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
