package catalogue

// AdminPrices uses only current city price rows loaded by ServiceBundle.
// Missing prices stay missing; a partner price is never used as a fallback.
func AdminPrices(b *ServiceBundle) ProPrices {
	out := ProPrices{}
	for _, o := range b.Options {
		if o.Active && o.Price != nil {
			out[o.ID] = ItemPrice{ID: o.Price.ID, PricePaise: o.Price.PricePaise, Unit: o.Unit}
		}
	}
	for _, g := range b.Groups {
		if g.Active {
			for _, a := range g.Addons {
				if a.Active && a.Price != nil {
					out[a.ID] = ItemPrice{ID: a.Price.ID, PricePaise: a.Price.PricePaise, Unit: UnitPerJob}
				}
			}
		}
	}
	return out
}
