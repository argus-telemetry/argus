package main

// Import collector plugins to trigger their init() registration.
import (
	_ "github.com/argus-5g/argus/plugins/free5gc"
	_ "github.com/argus-5g/argus/plugins/gnmi"
	_ "github.com/argus-5g/argus/plugins/open5gs"
)
