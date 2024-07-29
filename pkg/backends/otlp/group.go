package otlp

import (
	"golang.org/x/exp/maps"

	"github.com/atlassian/gostatsd/pkg/backends/otlp/internal/data"
)

type group map[uint64]data.ResourceMetrics

// Group is used to ensure metrics that have the same resource attributes
// are grouped together and it uses a fixed values to reduce potential memory
// allocations compared to using a string value
type Group struct {
	batches         []group
	metricsInserted int
	batchSize       int
}

func NewGroup(batchSize int) Group {
	return Group{
		batches:   []group{make(group)},
		batchSize: batchSize,
	}
}

func (g *group) Values() []data.ResourceMetrics {
	return maps.Values(*g)
}

func (g *Group) Insert(is data.InstrumentationScope, resources data.Map, m data.Metric) {
	key := resources.Hash()

	currentBatch := g.batches[len(g.batches)-1]
	entry, exist := (currentBatch)[key]
	if !exist {
		entry = data.NewResourceMetrics(
			data.NewResource(
				data.WithResourceMap(resources),
			),
		)
		(currentBatch)[key] = entry
	}
	entry.AppendMetric(is, m)
	currentBatch[key] = entry

	if len(currentBatch) == g.batchSize {
		g.batches = append(g.batches, make(group))
	}
}
