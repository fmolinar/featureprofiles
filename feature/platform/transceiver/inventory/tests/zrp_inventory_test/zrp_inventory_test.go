package zrp_inventory_test

import (
	"flag"
	"reflect"
	"testing"
	"time"

	"github.com/openconfig/featureprofiles/internal/cfgplugins"
	"github.com/openconfig/featureprofiles/internal/components"
	"github.com/openconfig/featureprofiles/internal/deviations"
	"github.com/openconfig/featureprofiles/internal/fptest"
	"github.com/openconfig/featureprofiles/internal/samplestream"
	"github.com/openconfig/ondatra"
	"github.com/openconfig/ondatra/gnmi"
	"github.com/openconfig/ondatra/gnmi/oc"
	"github.com/openconfig/ygot/ygot"
)

const (
	samplingInterval  = 10 * time.Second
	timeout           = 10 * time.Minute
	waitInterval      = 30 * time.Second
	targetOutputPower = -3
	frequency         = 193100000
)

var (
	operationalModeFlag = flag.Int("operational_mode", 5, "vendor-specific operational-mode for the channel")
	operationalMode     uint16
)

func TestMain(m *testing.M) {
	fptest.RunTests(m)
}

func verifyAllInventoryValues(t *testing.T, pStreamsStr []*samplestream.SampleStream[string], pStreamsUnion []*samplestream.SampleStream[oc.Component_Type_Union]) {
	for _, stream := range pStreamsStr {
		inventoryStr := stream.Next()
		if inventoryStr == nil {
			t.Fatalf("Inventory telemetry %v was not streamed in the most recent subscription interval", stream)
		}
		inventoryVal, ok := inventoryStr.Val()
		if !ok {
			t.Fatalf("Inventory telemetry %q is not present or valid, expected <string>", inventoryStr)
		}
		if reflect.TypeOf(inventoryVal).Kind() != reflect.String {
			t.Fatalf("Return value is not type string")
		} else {
			t.Logf("Inventory telemetry %q is valid: %q", inventoryStr, inventoryVal)
		}
	}

	for _, stream := range pStreamsUnion {
		inventoryUnion := stream.Next()
		if inventoryUnion == nil {
			t.Fatalf("Inventory telemetry %v was not streamed in the most recent subscription interval", stream)
		}
		inventoryVal, ok := inventoryUnion.Val()
		if !ok {
			t.Fatalf("Inventory telemetry %q is not present or valid, expected <union>", inventoryUnion)
		} else {
			t.Logf("Inventory telemetry %q is valid: %q", inventoryUnion, inventoryVal)
		}

	}
}

func TestInventoryInterfaceFlap(t *testing.T) {
	if operationalModeFlag != nil {
		operationalMode = uint16(*operationalModeFlag)
	} else {
		t.Fatalf("Please specify the vendor-specific operational-mode flag")
	}
	dut := ondatra.DUT(t, "dut")
	dp1 := dut.Port(t, "port1")
	dp2 := dut.Port(t, "port2")
	tr1 := gnmi.Get(t, dut, gnmi.OC().Interface(dp1.Name()).Transceiver().State())
	// tr2 := gnmi.Get(t, dut, gnmi.OC().Interface(dp2.Name()).Transceiver().State())
	och1 := components.OpticalChannelComponentFromPort(t, dut, dp1)
	och2 := components.OpticalChannelComponentFromPort(t, dut, dp2)
	fptest.ConfigureDefaultNetworkInstance(t, dut)
	cfgplugins.ConfigOpticalChannel(t, dut, och1, frequency, targetOutputPower, operationalMode)
	cfgplugins.ConfigOpticalChannel(t, dut, och2, frequency, targetOutputPower, operationalMode)

	// Uncomment once the Ondatra OC release version is fixed.
	// if (dp1.PMD() != ondatra.PMD400GBASEZRP) || (dp2.PMD() != ondatra.PMD400GBASEZRP) {
	// 	t.Fatalf("Transceivers types (%v, %v): (%v, %v) are not 400ZR_PLUS, expected %v", tr1, tr2, dp1.PMD(), dp2.PMD(), ondatra.PMD400GBASEZRP)
	// }
	component1 := gnmi.OC().Component(tr1)

	// Wait for channels to be up.
	gnmi.Await(t, dut, gnmi.OC().Interface(dp1.Name()).OperStatus().State(), timeout, oc.Interface_OperStatus_UP)
	gnmi.Await(t, dut, gnmi.OC().Interface(dp2.Name()).OperStatus().State(), timeout, oc.Interface_OperStatus_UP)

	var p1StreamsStr []*samplestream.SampleStream[string]
	var p1StreamsUnion []*samplestream.SampleStream[oc.Component_Type_Union]

	// TODO: b/333021032 - Uncomment the description check from the test once the bug is fixed.
	p1StreamsStr = append(p1StreamsStr,
		samplestream.New(t, dut, component1.SerialNo().State(), samplingInterval),
		samplestream.New(t, dut, component1.PartNo().State(), samplingInterval),
		samplestream.New(t, dut, component1.MfgName().State(), samplingInterval),
		samplestream.New(t, dut, component1.HardwareVersion().State(), samplingInterval),
		samplestream.New(t, dut, component1.FirmwareVersion().State(), samplingInterval),
		// samplestream.New(t, dut, component1.Description().State(), samplingInterval),
	)
	if !deviations.ComponentMfgDateUnsupported(dut) {
		p1StreamsStr = append(p1StreamsStr, samplestream.New(t, dut, component1.MfgDate().State(), samplingInterval))
	}
	p1StreamsUnion = append(p1StreamsUnion, samplestream.New(t, dut, component1.Type().State(), samplingInterval))

	verifyAllInventoryValues(t, p1StreamsStr, p1StreamsUnion)

	// Disable or shut down the interface on the DUT.
	for _, p := range dut.Ports() {
		cfgplugins.ToggleInterface(t, dut, p.Name(), false)
	}
	// Wait for channels to be down.
	gnmi.Await(t, dut, gnmi.OC().Interface(dp1.Name()).OperStatus().State(), timeout, oc.Interface_OperStatus_DOWN)
	gnmi.Await(t, dut, gnmi.OC().Interface(dp2.Name()).OperStatus().State(), timeout, oc.Interface_OperStatus_DOWN)

	t.Logf("Interfaces are down: %v, %v", dp1.Name(), dp2.Name())
	verifyAllInventoryValues(t, p1StreamsStr, p1StreamsUnion)

	time.Sleep(waitInterval)
	// Re-enable interfaces.
	for _, p := range dut.Ports() {
		cfgplugins.ToggleInterface(t, dut, p.Name(), true)
	}
	// Wait for channels to be up.
	gnmi.Await(t, dut, gnmi.OC().Interface(dp1.Name()).OperStatus().State(), timeout, oc.Interface_OperStatus_UP)
	gnmi.Await(t, dut, gnmi.OC().Interface(dp2.Name()).OperStatus().State(), timeout, oc.Interface_OperStatus_UP)

	t.Logf("Interfaces are up: %v, %v", dp1.Name(), dp2.Name())
	verifyAllInventoryValues(t, p1StreamsStr, p1StreamsUnion)
}

func TestInventoryTransceiverOnOff(t *testing.T) {
	if operationalModeFlag != nil {
		operationalMode = uint16(*operationalModeFlag)
	} else {
		t.Fatalf("Please specify the vendor-specific operational-mode flag")
	}
	dut := ondatra.DUT(t, "dut")
	dp1 := dut.Port(t, "port1")
	dp2 := dut.Port(t, "port2")
	tr1 := gnmi.Get(t, dut, gnmi.OC().Interface(dp1.Name()).Transceiver().State())
	tr2 := gnmi.Get(t, dut, gnmi.OC().Interface(dp2.Name()).Transceiver().State())
	och1 := components.OpticalChannelComponentFromPort(t, dut, dp1)
	och2 := components.OpticalChannelComponentFromPort(t, dut, dp2)
	fptest.ConfigureDefaultNetworkInstance(t, dut)
	cfgplugins.ConfigOpticalChannel(t, dut, och1, frequency, targetOutputPower, operationalMode)
	cfgplugins.ConfigOpticalChannel(t, dut, och2, frequency, targetOutputPower, operationalMode)

	// Uncomment once the Ondatra OC release version is fixed.
	// if (dp1.PMD() != ondatra.PMD400GBASEZRP) || (dp2.PMD() != ondatra.PMD400GBASEZRP) {
	// 	t.Fatalf("Transceivers types (%v, %v): (%v, %v) are not 400ZR_PLUS, expected %v", tr1, tr2, dp1.PMD(), dp2.PMD(), ondatra.PMD400GBASEZRP)
	// }
	component1 := gnmi.OC().Component(tr1)

	// Wait for channels to be up.
	gnmi.Await(t, dut, gnmi.OC().Interface(dp1.Name()).OperStatus().State(), timeout, oc.Interface_OperStatus_UP)
	gnmi.Await(t, dut, gnmi.OC().Interface(dp2.Name()).OperStatus().State(), timeout, oc.Interface_OperStatus_UP)

	var p1StreamsStr []*samplestream.SampleStream[string]
	var p1StreamsUnion []*samplestream.SampleStream[oc.Component_Type_Union]

	// TODO: b/333021032 - Uncomment the description check from the test once the bug is fixed.
	p1StreamsStr = append(p1StreamsStr,
		samplestream.New(t, dut, component1.SerialNo().State(), samplingInterval),
		samplestream.New(t, dut, component1.PartNo().State(), samplingInterval),
		samplestream.New(t, dut, component1.MfgName().State(), samplingInterval),
		samplestream.New(t, dut, component1.HardwareVersion().State(), samplingInterval),
		samplestream.New(t, dut, component1.FirmwareVersion().State(), samplingInterval),
		// samplestream.New(t, dut, component1.Description().State(), samplingInterval),
	)
	if !deviations.ComponentMfgDateUnsupported(dut) {
		p1StreamsStr = append(p1StreamsStr, samplestream.New(t, dut, component1.MfgDate().State(), samplingInterval))
	}
	p1StreamsUnion = append(p1StreamsUnion, samplestream.New(t, dut, component1.Type().State(), samplingInterval))

	verifyAllInventoryValues(t, p1StreamsStr, p1StreamsUnion)

	//  power off interface transceiver.
	for _, p := range dut.Ports() {
		// for transceiver disable, the input needs to be the transceiver name instead of the interface name
		tr := gnmi.Get(t, dut, gnmi.OC().Interface(p.Name()).Transceiver().State())
		gnmi.Update(t, dut, gnmi.OC().Component(p.Name()).Name().Config(), p.Name())
		setConfigLeaf := gnmi.OC().Component(tr)
		gnmi.Update(t, dut, setConfigLeaf.Config(), &oc.Component{
			Name: ygot.String(tr),
		})
		gnmi.Update(t, dut, gnmi.OC().Component(tr).Transceiver().Enabled().Config(), false)
	}
	t.Logf("Interfaces are down: %v, %v", dp1.Name(), dp2.Name())
	verifyAllInventoryValues(t, p1StreamsStr, p1StreamsUnion)

	time.Sleep(3 * waitInterval)
	//  power on interface transceiver.
	gnmi.Update(t, dut, gnmi.OC().Component(dp1.Name()).Name().Config(), dp1.Name())
	gnmi.Update(t, dut, gnmi.OC().Component(tr1).Transceiver().Enabled().Config(), true)
	gnmi.Update(t, dut, gnmi.OC().Component(dp2.Name()).Name().Config(), dp2.Name())
	gnmi.Update(t, dut, gnmi.OC().Component(tr2).Transceiver().Enabled().Config(), true)
	// Wait for channels to be up.
	gnmi.Await(t, dut, gnmi.OC().Interface(dp1.Name()).OperStatus().State(), timeout, oc.Interface_OperStatus_UP)
	gnmi.Await(t, dut, gnmi.OC().Interface(dp2.Name()).OperStatus().State(), timeout, oc.Interface_OperStatus_UP)

	t.Logf("Interfaces are up: %v, %v", dp1.Name(), dp2.Name())
	verifyAllInventoryValues(t, p1StreamsStr, p1StreamsUnion)
}
