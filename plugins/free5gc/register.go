package free5gc

import "github.com/argus-5g/argus/internal/collector"

func init() {
	collector.DefaultRegistry.Register("free5gc-amf", func() collector.Collector { return &Collector{nfType: "AMF"} })
	collector.DefaultRegistry.Register("free5gc-smf", func() collector.Collector { return &Collector{nfType: "SMF"} })
	collector.DefaultRegistry.Register("free5gc-upf", func() collector.Collector { return &Collector{nfType: "UPF"} })
}
