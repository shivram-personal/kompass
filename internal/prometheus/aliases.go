package prometheus

import "github.com/skyhook-io/radar/pkg/prom"

// Types re-exported from pkg/prom so existing internal/prometheus consumers
// (internal/opencost, internal/server, etc.) do not need to change imports.
type (
	QueryResult = prom.QueryResult
	Series      = prom.Series
	DataPoint   = prom.DataPoint
	ServiceInfo = prom.ServiceInfo
	Status      = prom.Status
)

// Metric category types + constants re-exported for existing callers.
type MetricCategory = prom.MetricCategory

const (
	CategoryCPU        = prom.CategoryCPU
	CategoryMemory     = prom.CategoryMemory
	CategoryNetworkRX  = prom.CategoryNetworkRX
	CategoryNetworkTX  = prom.CategoryNetworkTX
	CategoryFilesystem = prom.CategoryFilesystem
)

// Query builder functions re-exported from pkg/prom.
var (
	SanitizeLabelValue                = prom.SanitizeLabelValue
	BuildQuery                        = prom.BuildQuery
	BuildQueryNoContainerFilter       = prom.BuildQueryNoContainerFilter
	BuildNamespaceQuery               = prom.BuildNamespaceQuery
	BuildNamespaceQueryNoContainerFilter = prom.BuildNamespaceQueryNoContainerFilter
	BuildClusterQuery                 = prom.BuildClusterQuery
	BuildClusterQueryNoContainerFilter = prom.BuildClusterQueryNoContainerFilter
	AllCategories                     = prom.AllCategories
	SupportedKinds                    = prom.SupportedKinds
	CategoriesForKind                 = prom.CategoriesForKind
	CategoryLabel                     = prom.CategoryLabel
	CategoryUnit                      = prom.CategoryUnit
	CategoryUnitForKind               = prom.CategoryUnitForKind
	CategoryUsesContainerFilter       = prom.CategoryUsesContainerFilter
)

// categoryUsesContainerFilter is a lowercase alias kept for handlers.go's
// existing call site; delete when call sites migrate to the capitalized form.
var categoryUsesContainerFilter = prom.CategoryUsesContainerFilter
