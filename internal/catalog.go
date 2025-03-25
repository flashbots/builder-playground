package internal

var components = []Service{}

func register(component Service) {
	components = append(components, component)
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
}

func FindComponent(name string) Service {
	for _, component := range components {
		if component.Name() == name {
			return component
		}
	}
	return nil
}
