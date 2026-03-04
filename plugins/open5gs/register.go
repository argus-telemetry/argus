package open5gs

import "github.com/argus-5g/argus/internal/collector"

func init() {
	collector.DefaultRegistry.Register("open5gs-amf", func() collector.Collector { return &Collector{nfType: "AMF"} })
	collector.DefaultRegistry.Register("open5gs-smf", func() collector.Collector { return &Collector{nfType: "SMF"} })
	collector.DefaultRegistry.Register("open5gs-upf", func() collector.Collector { return &Collector{nfType: "UPF"} })
}
