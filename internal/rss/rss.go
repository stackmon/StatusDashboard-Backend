package rss

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/feeds"
	"go.uber.org/zap"

	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/baseurl"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

const generalTitle = "Incidents | Status Dashboard"
const maxEvents = 10

var errRSSWrongParams = fmt.Errorf( //nolint:staticcheck
	"Status Dashboard RSS feed\nPlease read the documentation to\nmake the correct request",
)

const (
	maintenanceImpactTextRepr = "Scheduled maintenance"
	minorImpactTextRepr       = "Minor incident (i.e. performance impact)"
	majorImpactTextRepr       = "Major incident"
	outageImpactTextRepr      = "Service outage"
)

// getIncidentImpactsStr returns a string representation of the incident impact level.
// The numeric values (0,1,2,3) represent specific impact levels for the incident.
func getIncidentImpactsStr(impact int) string {
	switch impact {
	case 1:
		return minorImpactTextRepr
	case 2: //nolint:mnd
		return majorImpactTextRepr
	}

	return outageImpactTextRepr
}

func HandleRSS(dbInst *db.DB, logger *zap.Logger, publicBaseURL string) gin.HandlerFunc {
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

		// Postgres rejects NUL bytes and invalid UTF-8 in text values; reject
		// such filter input here instead of surfacing raw database errors.
		if strings.ContainsRune(componentName, 0) || !utf8.ValidString(componentName) {
			apiErrors.RaiseBadRequestErr(c, errRSSWrongParams)
			return
		}

		base := baseurl.Resolve(c.Request, publicBaseURL)
		params := feedParams{
			region:        region,
			componentName: componentName,
		}

		events, err := getEvents(c.Request.Context(), dbInst, logger, params, maxEvents)
		if err != nil {
			// Only static, expected errors may reach the client; raw database
			// errors (e.g. SQLSTATE details) must not be echoed back.
			if componentName != "" && errors.Is(err, db.ErrDBComponentDSNotExist) {
				apiErrors.RaiseStatusNotFoundErr(c, err)
				return
			}
			apiErrors.RaiseInternalErr(c, err)
			return
		}

		feed := buildFeed(prepareFeedTitle(region, componentName), base, c.Request, sortEvents(events))

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
}

func getEvents(
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
		incidents, err = dbInstance.GetEvents(ctx, false, incParams)
		if err != nil {
			return nil, err
		}
	}

	return incidents, nil
}

// buildFeed assembles the feed. Its links are absolute so that a reader keeps
// working outside the host the feed was fetched from.
func buildFeed(title string, base *url.URL, req *http.Request, events []*db.Incident) *feeds.Feed {
	feed := &feeds.Feed{
		Title:       title,
		Link:        &feeds.Link{Href: baseurl.Self(base, req), Rel: "self"},
		Description: title + " - Incidents",
		Created:     time.Now(),
	}

	var feedItems []*feeds.Item
	for _, e := range events {
		feedItems = append(feedItems, createFeedItems(e, base)...)
	}

	feed.Items = feedItems

	return feed
}

func createFeedItems(incident *db.Incident, base *url.URL) []*feeds.Item {
	if incident.Type == event.TypeMaintenance {
		return createMaintenanceFeedItems(incident, base)
	}

	if incident.Type == event.TypeInformation {
		//TODO: placeholder for information type events
		return nil
	}

	return createIncidentFeedItems(incident, base)
}

func createIncidentFeedItems(incident *db.Incident, base *url.URL) []*feeds.Item {
	var feedItems []*feeds.Item

	impact := getIncidentImpactsStr(*incident.Impact)

	// Events may have no components (e.g. synthetic system events), so the
	// title and description must not assume the component list is non-empty.
	var title string
	switch len(incident.Components) {
	case 0:
		title = fmt.Sprintf("Status change to %s", impact)
	case 1:
		title = fmt.Sprintf("%s status changed to %s", incident.Components[0].Name, impact)
	default:
		title = fmt.Sprintf("Status change of multiple services to %s", impact)
	}

	var description strings.Builder
	startDate := *incident.StartDate

	if len(incident.Components) == 0 {
		fmt.Fprintf(&description, "A %s was detected at %s UTC.", impact, startDate.Format(time.DateTime))
	} else {
		fmt.Fprintf(&description, "A %s was detected at %s UTC for ", impact, startDate.Format(time.DateTime))

		for i := range len(incident.Components) {
			c := incident.Components[i]
			fmt.Fprintf(&description, "%s in %s", c.Name, c.Region())
			if i != len(incident.Components)-1 {
				description.WriteString(", ")
			} else {
				description.WriteString(".")
			}
		}
	}

	if incident.Description != nil && *incident.Description != "" {
		// Append the main description if it exists.
		fmt.Fprintf(&description, " %s", *incident.Description)
	}

	item := &feeds.Item{
		Title:       title,
		Link:        &feeds.Link{Href: baseurl.Link(base, fmt.Sprintf("/incidents/%d", incident.ID))},
		Created:     *incident.StartDate,
		Description: description.String(),
	}

	feedItems = append(feedItems, item)

	// add updates
	for _, s := range incident.Statuses {
		// Skip the initial detected status, as it is already included in the main item.
		if s.Status == event.IncidentDetected {
			continue
		}

		d := fmt.Sprintf("An update was provided at %s UTC", s.Timestamp.Format(time.DateTime))
		switch {
		case len(incident.Components) == 0:
			d += ":"
		case len(incident.Components) > 1:
			d += " for"
			for _, c := range incident.Components {
				d += fmt.Sprintf(" %s (%s),", c.Name, c.Region())
			}
			d = strings.TrimSuffix(d, ",") + ":"
		default:
			d += fmt.Sprintf(" for %s in %s:",
				incident.Components[0].Name, incident.Components[0].Region())
		}
		d += fmt.Sprintf(" %s - %s", s.Status, s.Text)

		upd := &feeds.Item{
			Title:       fmt.Sprintf("Update published for: %s", *incident.Text),
			Link:        &feeds.Link{Href: baseurl.Link(base, fmt.Sprintf("/incidents/%d", incident.ID))},
			Created:     s.Timestamp,
			Description: d,
		}

		feedItems = append(feedItems, upd)
	}

	return feedItems
}

func createMaintenanceFeedItems(maintenance *db.Incident, base *url.URL) []*feeds.Item {
	var feedItems []*feeds.Item

	compTypes := make([]string, 0, len(maintenance.Components))
	compNames := make([]string, 0, len(maintenance.Components))

	for _, c := range maintenance.Components {
		compTypes = append(compTypes, c.Type())
		compNames = append(compNames, fmt.Sprintf("%s (%s)", c.Name, c.Region()))
	}
	compShortNames := strings.Join(compTypes, ", ")
	compLongNames := strings.Join(compNames, ", ")

	// Get the main description for the maintenance event.
	var genDesc string
	for _, s := range maintenance.Statuses {
		if s.Status == event.MaintenancePlanned {
			genDesc = s.Text
			break
		}
	}
	if maintenance.Description != nil && *maintenance.Description != "" {
		genDesc = *maintenance.Description
	}

	for _, s := range maintenance.Statuses {
		var title string
		var description string

		switch s.Status {
		case event.MaintenancePlanned:
			title = fmt.Sprintf("Maintenance planned for %s", compShortNames)
			description = fmt.Sprintf("A maintenance is planned for %s between %s UTC and %s UTC: %s",
				compLongNames,
				maintenance.StartDate.Format(time.DateTime),
				maintenance.EndDate.Format(time.DateTime),
				s.Text,
			)
		case event.MaintenanceInProgress:
			title = fmt.Sprintf("Maintenance started for %s", compShortNames)
			description = fmt.Sprintf("A maintenance started for %s planned until %s UTC: %s",
				compLongNames,
				maintenance.EndDate.Format(time.DateTime),
				genDesc,
			)
		case event.MaintenanceModified:
			title = fmt.Sprintf("Maintenance modified for %s", compShortNames)
			description = fmt.Sprintf("A maintenance modified for %s: %s",
				compLongNames,
				s.Text,
			)
		case event.MaintenanceCompleted:
			title = fmt.Sprintf("Maintenance completed for %s", compShortNames)
			description = fmt.Sprintf("A maintenance completed for %s.",
				compLongNames,
			)
		case event.MaintenanceCancelled:
			title = fmt.Sprintf("Maintenance cancelled for %s", compShortNames)
			description = fmt.Sprintf("A maintenance cancelled for %s.",
				compLongNames,
			)
		default:
			// skip unknown statuses and status "description"
			continue
		}

		upd := &feeds.Item{
			Title:       title,
			Link:        &feeds.Link{Href: baseurl.Link(base, fmt.Sprintf("/incidents/%d", maintenance.ID))},
			Created:     s.Timestamp,
			Description: description,
		}

		feedItems = append(feedItems, upd)
	}

	return feedItems
}

func prepareFeedTitle(region string, component string) string {
	if region == "" {
		return generalTitle
	}

	if component != "" {
		return fmt.Sprintf("%s (%s) - %s", component, region, generalTitle)
	}

	return fmt.Sprintf("%s - %s", region, generalTitle)
}

func sortEvents(incidents []*db.Incident) []*db.Incident {
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
