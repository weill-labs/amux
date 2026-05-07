package server

import (
	"reflect"
	"testing"
)

func TestServerTestArgsWithCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "replaces equals form",
			args: []string{"-test.v=true", "-test.count=10", "-test.run=TestFoo"},
			want: []string{"-test.v=true", "-test.count=1", "-test.run=TestFoo"},
		},
		{
			name: "replaces separated form",
			args: []string{"-test.count", "10", "-test.run=TestFoo"},
			want: []string{"-test.count=1", "-test.run=TestFoo"},
		},
		{
			name: "appends when absent",
			args: []string{"-test.run=TestFoo"},
			want: []string{"-test.run=TestFoo", "-test.count=1"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := serverTestArgsWithCount(tt.args, 1); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("serverTestArgsWithCount() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
