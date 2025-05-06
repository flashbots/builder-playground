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
	register(&RollupBoost{})
	register(&OpReth{})
	register(&BuilderHub{})
	register(&BuilderHubPostgres{})
	register(&BuilderHubMockProxy{})
	register(&nullService{})
	register(&OpTalos{})
	register(&AssertionDA{})
	register(&Faucet{})
}

func FindComponent(name string) ServiceGen {
	for _, component := range Components {
		if component.Name() == name {
			return component
		}
	}
	return nil
}
