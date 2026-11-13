package notification

import (
	"testing"

	"github.com/stretchr/testify/assert"
	mail "github.com/wneessen/go-mail"
)

func TestTLSPolicy(t *testing.T) {
	cases := map[TLSMode]mail.TLSPolicy{
		"":               mail.TLSMandatory,
		TLSRequired:      mail.TLSMandatory,
		TLSOpportunistic: mail.TLSOpportunistic,
		TLSNone:          mail.NoTLS,
	}

	for mode, want := range cases {
		assert.Equal(t, want, tlsPolicy(mode), "mode %q", mode)
	}
}
