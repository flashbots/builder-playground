package internal

var Components = []Service{}

func register(component Service) {
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
	register(&OrderflowProxySender{})
}

func FindComponent(name string) Service {
	for _, component := range Components {
		if component.Name() == name {
			return component
		}
	}
	return nil
}
