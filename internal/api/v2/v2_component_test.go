package v2

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
)

func TestToAPIComponentAlwaysEmitsAttributes(t *testing.T) {
	t.Parallel()

	comp := toAPIComponent(db.Component{ID: 164, Name: "Cloud Container Service"})

	require.NotNil(t, comp.Attributes)
	assert.Empty(t, comp.Attributes)

	payload, err := json.Marshal(comp)
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"id":164,"name":"Cloud Container Service","attributes":[]}`,
		string(payload),
	)
}

func TestToAPIComponentMapsAttributes(t *testing.T) {
	t.Parallel()

	comp := toAPIComponent(db.Component{
		ID:   287,
		Name: "Cloud Container Engine",
		Attrs: []db.ComponentAttr{
			{Name: "category", Value: "Container"},
			{Name: "region", Value: "EU-DE"},
			{Name: "type", Value: "cce"},
		},
	})

	assert.Equal(t, 287, comp.ID)
	assert.Equal(t, "Cloud Container Engine", comp.Name)
	assert.ElementsMatch(t, []ComponentAttribute{
		{Name: "category", Value: "Container"},
		{Name: "region", Value: "EU-DE"},
		{Name: "type", Value: "cce"},
	}, comp.Attributes)
}
