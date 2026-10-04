package rss

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/feeds"
	"go.uber.org/zap"

	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

const generalTitle = "Incidents | Status Dashboard"
const maxIncidents = 10

var errRSSWrongParams = fmt.Errorf( //nolint:staticcheck
	"Status Dashboard RSS feed\nPlease read the documentation to\nmake the correct request",
)

func HandleRSS(dbInst *db.DB, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		region := c.Query("mt")
		componentName := c.Query("srv")
		logger.Info("rss feed requested", zap.String("region", region), zap.String("component", componentName))

		if componentName != "" && region == "" {
			apiErrors.RaiseStatusNotFoundErr(c, errRSSWrongParams)
			return
		}

		if region != "" && !validateRegion(region) {
			apiErrors.RaiseStatusNotFoundErr(c, fmt.Errorf("the region '%s' is not valid", region))
			return
		}

		baseURL := fmt.Sprintf("%s://%s", c.Request.URL.Scheme, c.Request.Host)
		params := feedParams{
			region:        region,
			componentName: componentName,
			baseURL:       baseURL,
		}

		incidents, err := getIncidents(c.Request.Context(), dbInst, logger, params, maxIncidents)
		if err != nil {
			if componentName != "" {
				apiErrors.RaiseStatusNotFoundErr(c, err)
				return
			}
			apiErrors.RaiseInternalErr(c, err)
			return
		}

		feedTitle := prepareTitle(region, componentName)

		feed := &feeds.Feed{
			Title:       feedTitle,
			Link:        &feeds.Link{Href: baseURL + "/rss/" + c.Request.URL.RawQuery, Rel: "self"},
			Description: feedTitle + " - Incidents",
			Created:     time.Now(),
		}

		incidents = sortIncidents(incidents)
		feedItems := make([]*feeds.Item, 0, maxIncidents)

		for _, incident := range incidents {
			item := createFeedItem(incident, baseURL)
			feedItems = append(feedItems, item)
		}

		feed.Items = feedItems

		rss, err := feed.ToRss()
		if err != nil {
			apiErrors.RaiseInternalErr(c, err)
			return
		}

		c.Header("Content-Type", "application/rss+xml")
		c.String(http.StatusOK, rss)
	}
}

// TODO: add list of valid regions to the config or to the db
func validateRegion(region string) bool {
	regions := [4]string{"EU-DE", "EU-NL", "EU-CH2", "Global"}
	for _, r := range regions {
		if r == region {
			return true
		}
	}
	return false
}

type feedParams struct {
	region        string
	componentName string
	baseURL       string
}

func getIncidents(
	ctx context.Context, dbInstance *db.DB, log *zap.Logger, params feedParams, maxIncidents int,
) ([]*db.Incident, error) {
	var incidents []*db.Incident
	var err error

	incParams := &db.IncidentsParams{LastCount: maxIncidents}

	switch {
	case params.componentName != "" && params.region != "":
		attr := &db.ComponentAttr{
			Name:  "region",
			Value: params.region,
		}

		var component *db.Component
		component, err = dbInstance.GetComponentFromNameAttrs(ctx, params.componentName, attr)
		if err != nil {
			log.Error("failed to get component", zap.Error(err))
			return nil, err
		}

		incidents, err = dbInstance.GetEventsByComponentID(ctx, component.ID, incParams)
		if err != nil {
			return nil, err
		}
	case params.componentName == "" && params.region != "":
		attr := &db.ComponentAttr{
			Name:  "region",
			Value: params.region,
		}

		incidents, err = dbInstance.GetIncidentsByComponentAttr(ctx, attr, incParams)
		if err != nil {
			return nil, err
		}
	default:
		incidents, err = dbInstance.GetEvents(ctx, db.PublicAccess, incParams)
		if err != nil {
			return nil, err
		}
	}

	return incidents, nil
}

func createFeedContent(incident *db.Incident) string {
	var content string

	if len(incident.Statuses) > 0 {
		for i := len(incident.Statuses) - 1; i >= 0; i-- {
			status := incident.Statuses[i]
			if incident.Type == event.TypeMaintenance &&
				(status.Status == event.MaintenancePendingReview || status.Status == event.MaintenanceReviewed) {
				continue
			}
			content += fmt.Sprintf(
				"<small>%s</small><br><strong>%s - </strong>%s<br><br><br>",
				status.Timestamp.Format("2006-01-02 15:04 MST"),
				status.Status,
				status.Text,
			)
		}
	}

	impactLevel := getIncidentImpacts()[*incident.Impact]

	content += fmt.Sprintf("Incident impact: %s<br>", impactLevel)
	content += fmt.Sprintf(
		"Start date: %s<br>",
		incident.StartDate.Format("2006-01-02 15:04:05 MST"),
	)
	if incident.EndDate != nil {
		content += fmt.Sprintf(
			"End date: %s<br>",
			incident.EndDate.Format("2006-01-02 15:04:05 MST"),
		)
	}
	content += "<br>We apologize for the inconvenience and will share an update once we have more information."
	return content
}

func createFeedItem(incident *db.Incident, baseURL string) *feeds.Item {
	item := &feeds.Item{
		Title:   fmt.Sprintln(*incident.Text),
		Link:    &feeds.Link{Href: fmt.Sprintf("%s/incidents/%d", baseURL, incident.ID)},
		Created: *incident.StartDate,
	}

	// Here we use Description for backward compatibility with SD2
	item.Description = createFeedContent(incident)

	if len(incident.Statuses) > 0 {
		item.Updated = incident.Statuses[len(incident.Statuses)-1].Timestamp
	}
	if incident.EndDate != nil {
		item.Updated = *incident.EndDate
	} else {
		item.Updated = *incident.StartDate
	}

	return item
}

func prepareTitle(region string, component string) string {
	if region != "" {
		if component != "" {
			return fmt.Sprintf("%s (%s) - %s", component, region, generalTitle)
		}
		return fmt.Sprintf("%s - %s", region, generalTitle)
	}

	return generalTitle
}

func sortIncidents(incidents []*db.Incident) []*db.Incident {
	openIncidents := make([]*db.Incident, 0)
	closedIncidents := make([]*db.Incident, 0)

	for _, incident := range incidents {
		if incident.EndDate == nil {
			openIncidents = append(openIncidents, incident)
		} else {
			closedIncidents = append(closedIncidents, incident)
		}
	}

	sort.Slice(openIncidents, func(i, j int) bool {
		return openIncidents[i].StartDate.After(*openIncidents[j].StartDate)
	})
	sort.Slice(closedIncidents, func(i, j int) bool {
		return closedIncidents[i].EndDate.After(*closedIncidents[j].EndDate)
	})
	return append(openIncidents, closedIncidents...)
}

// getIncidentImpacts returns a map of impact levels
// The numeric values (0,1,2,3) represent specific impact levels for the incident.
func getIncidentImpacts() map[int]string {
	return map[int]string{
		0: "Scheduled maintenance",
		1: "Minor incident (i.e. performance impact)",
		2: "Major incident",
		3: "Service outage",
	}
}
