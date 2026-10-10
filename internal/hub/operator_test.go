package hub

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// TestTheOperatorActsInEveryWebCall fails when a call the web listener serves names an acting
// agent that the operator interceptor does not overwrite.
func TestTheOperatorActsInEveryWebCall(t *testing.T) {
	checked := 0
	for proc := range webProcedures {
		name := protoreflect.FullName(strings.ReplaceAll(strings.TrimPrefix(proc, "/"), "/", "."))
		d, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
		if err != nil {
			t.Fatalf("%s: %v", proc, err)
		}
		method, ok := d.(protoreflect.MethodDescriptor)
		if !ok {
			t.Fatalf("%s is not a method", proc)
		}
		mt, err := protoregistry.GlobalTypes.FindMessageByName(method.Input().FullName())
		if err != nil {
			t.Fatalf("%s: %v", proc, err)
		}
		msg := mt.New()
		f := msg.Descriptor().Fields().ByName("agent")
		if f == nil {
			continue
		}
		msg.Set(f, protoreflect.ValueOfString("builder"))
		asOperator(msg.Interface(), "operator")
		if got := msg.Get(f).String(); got != "operator" {
			t.Errorf("%s: agent %q after the interceptor", proc, got)
		}
		checked++
	}
	if checked < 5 { // Post, Subscribe, ListSubscriptions, UnreadByRoom, MarkRoomRead
		t.Fatalf("only %d web calls name an agent", checked)
	}
}
