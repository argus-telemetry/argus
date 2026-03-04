package collector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubCollector is a minimal Collector implementation for registry tests.
type stubCollector struct {
	name string
}

func (s *stubCollector) Name() string                                       { return s.name }
func (s *stubCollector) Connect(_ context.Context, _ CollectorConfig) error { return nil }
func (s *stubCollector) Collect(_ context.Context, _ chan<- RawRecord) error { return nil }
func (s *stubCollector) Close() error                                       { return nil }

func TestRegistry_RegisterAndGet(t *testing.T) {
	tests := []struct {
		name     string
		register string
		get      string
		wantName string
	}{
		{
			name:     "register and retrieve by exact name",
			register: "free5gc-amf",
			get:      "free5gc-amf",
			wantName: "free5gc-amf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			r.Register(tt.register, func() Collector {
				return &stubCollector{name: tt.wantName}
			})

			c, err := r.Get(tt.get)
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, c.Name())
		})
	}
}

func TestRegistry_GetUnregistered(t *testing.T) {
	tests := []struct {
		name    string
		get     string
		wantErr string
	}{
		{
			name:    "nonexistent collector returns error",
			get:     "does-not-exist",
			wantErr: `collector "does-not-exist" not registered`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()

			c, err := r.Get(tt.get)
			require.Error(t, err)
			assert.Nil(t, c)
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestRegistry_List(t *testing.T) {
	tests := []struct {
		name     string
		register []string
		want     []string
	}{
		{
			name:     "two collectors listed in sorted order",
			register: []string{"open5gs-smf", "free5gc-amf"},
			want:     []string{"free5gc-amf", "open5gs-smf"},
		},
		{
			name:     "empty registry returns empty slice",
			register: nil,
			want:     []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			for _, n := range tt.register {
				n := n
				r.Register(n, func() Collector {
					return &stubCollector{name: n}
				})
			}

			got := r.List()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	tests := []struct {
		name      string
		register  string
		firstName string
		lastName  string
		wantName  string
	}{
		{
			name:      "second registration overwrites first",
			register:  "free5gc-amf",
			firstName: "v1",
			lastName:  "v2",
			wantName:  "v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			r.Register(tt.register, func() Collector {
				return &stubCollector{name: tt.firstName}
			})
			r.Register(tt.register, func() Collector {
				return &stubCollector{name: tt.lastName}
			})

			c, err := r.Get(tt.register)
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, c.Name())
		})
	}
}
