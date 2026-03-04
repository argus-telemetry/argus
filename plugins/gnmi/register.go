package gnmi

import "github.com/argus-5g/argus/internal/collector"

func init() {
	collector.DefaultRegistry.Register("gnmi-gnb", func() collector.Collector {
		return &Collector{nfType: "gNB"}
	})
}
