package plugin

func init() {
	Registry["reddit-promotions"] = func() ContentFilter {
		return NewInterceptionFilter(
			"reddit-promotions",
			"0.1.0",
			[]string{"www.reddit.com", "old.reddit.com"},
		)
	}
}
