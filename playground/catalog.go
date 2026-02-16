package playground

var Components = []ComponentGen{}

func register(component ComponentGen) {
	Components = append(Components, component)
}

func init() {
	register(&OpBatcher{})
	register(&OpGeth{})
	register(&OpNode{})
	register(&RethEL{})
	register(&LighthouseBeaconNode{})
	register(&LighthouseValidator{})
	register(&ClProxy{})
	register(&MevBoostRelay{})
	register(&MevBoost{})
	register(&RollupBoost{})
	register(&OpReth{})
	register(&nullService{})
	register(&OpRbuilder{})
	register(&FlashblocksRPC{})
	register(&Contender{})
	register(&BProxy{})
	register(&WebsocketProxy{})
	register(&BuilderHub{})
	register(&ChainMonitor{})
	register(&Bootnode{})
}
