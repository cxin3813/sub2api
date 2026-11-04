package repository

import (
	"reflect"
	"testing"
)

func TestBodyLogUsageDecoratorPreservesEveryRepositoryCapability(t *testing.T) {
	base := reflect.TypeOf((*usageLogRepository)(nil))
	observed := reflect.TypeOf((*observedUsageLogRepository)(nil))
	for i := 0; i < base.NumMethod(); i++ {
		method := base.Method(i)
		decorated, ok := observed.MethodByName(method.Name)
		if !ok {
			t.Errorf("body log decorator hid %s", method.Name)
			continue
		}
		// The receiver changes, but optional-interface callers must retain the
		// exact arguments and results, including query filters and dimensions.
		if method.Type.NumIn() != decorated.Type.NumIn() || method.Type.NumOut() != decorated.Type.NumOut() {
			t.Errorf("body log decorator changed the signature of %s", method.Name)
			continue
		}
		for arg := 1; arg < method.Type.NumIn(); arg++ {
			if method.Type.In(arg) != decorated.Type.In(arg) {
				t.Errorf("body log decorator changed argument %d of %s", arg, method.Name)
			}
		}
		for result := 0; result < method.Type.NumOut(); result++ {
			if method.Type.Out(result) != decorated.Type.Out(result) {
				t.Errorf("body log decorator changed result %d of %s", result, method.Name)
			}
		}
	}
}
