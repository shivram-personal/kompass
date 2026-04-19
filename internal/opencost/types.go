package opencost

// This file re-exports the cost-data types defined in radar/pkg/opencost so
// that internal/opencost HTTP handlers and other radar-local consumers keep
// working unchanged while the underlying types become the shared source of
// truth across radar, skyhook-connector, and koala-backend.

import "github.com/skyhook-io/radar/pkg/opencost"

// Unavailability reasons — returned in the "reason" field when available=false
// so the frontend can show contextual guidance to the user.
const (
	ReasonNoPrometheus = opencost.ReasonNoPrometheus
	ReasonNoMetrics    = opencost.ReasonNoMetrics
	ReasonQueryError   = opencost.ReasonQueryError
)

// Type aliases for the cost-data domain types.
type (
	CostSummary          = opencost.CostSummary
	NamespaceCost        = opencost.NamespaceCost
	WorkloadCostResponse = opencost.WorkloadCostResponse
	WorkloadCost         = opencost.WorkloadCost
	CostTrendResponse    = opencost.CostTrendResponse
	CostTrendSeries      = opencost.CostTrendSeries
	CostDataPoint        = opencost.CostDataPoint
	NodeCostResponse     = opencost.NodeCostResponse
	NodeCost             = opencost.NodeCost
)
