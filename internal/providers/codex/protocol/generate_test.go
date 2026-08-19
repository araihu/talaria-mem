package protocol

import (
	"context"
	"reflect"
	"testing"
)

func TestGeneratedClientSurface(t *testing.T) {
	clientType := reflect.TypeOf((*Client)(nil))
	for _, name := range []string{"Initialize", "ModelList", "ThreadRead", "ThreadFork", "ThreadStart", "TurnStart"} {
		if _, ok := clientType.MethodByName(name); !ok {
			t.Fatalf("generated Client missing %s", name)
		}
	}
	if reflect.TypeOf((*Client).Initialize).NumIn() != 3 {
		t.Fatal("Initialize wrapper does not expose context and params")
	}
	_ = context.Background()
}

func TestGeneratedNotificationDiscriminators(t *testing.T) {
	if NotificationTurnStarted != "turn/started" || NotificationAgentMessageDone != "item/completed" || NotificationTurnCompleted != "turn/completed" || NotificationError != "error" {
		t.Fatalf("notification constants drifted")
	}
}
