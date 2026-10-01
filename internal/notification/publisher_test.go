package notification

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublisher_AllowsDomain(t *testing.T) {
	enabled := NewPublisher(Config{
		Enabled:        true,
		AllowedDomains: []string{"example.com", "t-systems.com"},
	}, nil)

	t.Run("allowed domain passes", func(t *testing.T) {
		assert.True(t, enabled.AllowsDomain("user@example.com"))
		assert.True(t, enabled.AllowsDomain("User@T-Systems.com"), "case-insensitive")
	})

	t.Run("foreign domain is rejected", func(t *testing.T) {
		assert.False(t, enabled.AllowsDomain("user@gmail.com"))
	})

	t.Run("subdomain is not a match", func(t *testing.T) {
		assert.False(t, enabled.AllowsDomain("user@mail.example.com"))
	})

	t.Run("malformed address is rejected", func(t *testing.T) {
		assert.False(t, enabled.AllowsDomain("not-an-email"))
	})

	t.Run("empty allow-list permits any domain", func(t *testing.T) {
		p := NewPublisher(Config{Enabled: true}, nil)
		assert.True(t, p.AllowsDomain("user@anywhere.org"))
	})

	t.Run("disabled feature permits any domain", func(t *testing.T) {
		p := NewPublisher(Config{AllowedDomains: []string{"example.com"}}, nil)
		assert.True(t, p.AllowsDomain("user@gmail.com"))
	})

	t.Run("nil publisher permits any domain", func(t *testing.T) {
		var p *Publisher
		assert.True(t, p.AllowsDomain("user@gmail.com"))
	})
}
