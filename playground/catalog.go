package playground

var Components = []ServiceGen{}

func register(component ServiceGen) {
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
	register(&BuilderHub{})
	register(&BuilderHubPostgres{})
	register(&BuilderHubMockProxy{})
	register(&nullService{})
	register(&OpRbuilder{})
	register(&FlashblocksRPC{})
	register(&Contender{})
	register(&BProxy{})
	register(&WebsocketProxy{})
	register(&BuilderHub2{})
}
