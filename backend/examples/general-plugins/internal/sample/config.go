package sample

import (
	"context"
	"encoding/json"
	examples "github.com/Wei-Shaw/sub2api/examples/general-plugins"
	"math"
	"net/url"
	"regexp"
	"strings"
)

const MaxContentBytes = 1024 * 1024
const MaxJSONBytes = 64 * 1024
const maxRules = 32

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
var sourceKey = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,127}$`)
var pricingFields = map[string]bool{"input_price": true, "output_price": true, "cache_write_price": true, "cache_read_price": true, "per_request_price": true}

type credentialRule struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Pattern  string `json:"pattern"`
	compiled *regexp.Regexp
}
type credentialConfig struct {
	Version int64            `json:"config_version"`
	Rules   []credentialRule `json:"rules"`
}
type businessRule struct {
	ID       string   `json:"id"`
	Version  string   `json:"version"`
	Keywords []string `json:"keywords"`
	Weight   float64  `json:"weight"`
}
type businessConfig struct {
	Version   int64          `json:"config_version"`
	Threshold float64        `json:"threshold"`
	Rules     []businessRule `json:"rules"`
}
type pricingRule struct {
	ChannelID      int64    `json:"channel_id"`
	PricingID      int64    `json:"pricing_id"`
	SourceKey      string   `json:"source_key"`
	Field          string   `json:"field"`
	Multiplier     float64  `json:"multiplier"`
	MaxPrice       float64  `json:"max_price"`
	MaxChangeRatio *float64 `json:"max_change_ratio"`
}
type pricingConfig struct {
	Version   int64         `json:"config_version"`
	SourceURL string        `json:"source_url"`
	Rules     []pricingRule `json:"rules"`
}

func positive(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) && n > 0 && n <= 1e6 }
func (s *Server) loadConfig(ctx context.Context, b []byte) error {
	provided, e := object(ctx, b)
	if e != nil {
		return e
	}
	for _, v := range provided {
		if v == nil {
			return errJSON
		}
	}
	_, replacesRules := provided["rules"]
	defaults, e := examples.Source(s.name, "config.example.json")
	if e != nil {
		return errJSON
	}
	switch s.name {
	case "credential-input":
		var c credentialConfig
		if json.Unmarshal(defaults, &c) != nil {
			return errJSON
		}
		if replacesRules {
			c.Rules = nil
		}
		if strict(ctx, b, &c) != nil || c.Version != 1 || len(c.Rules) == 0 || len(c.Rules) > maxRules {
			return errJSON
		}
		seen := map[string]bool{}
		for i := range c.Rules {
			r := &c.Rules[i]
			if !identifier.MatchString(r.ID) || !identifier.MatchString(r.Version) || seen[r.ID] || len(r.Pattern) == 0 || len(r.Pattern) > 256 {
				return errJSON
			}
			seen[r.ID] = true
			r.compiled, e = regexp.Compile(r.Pattern)
			if e != nil || r.compiled.MatchString("") {
				return errJSON
			}
		}
		s.credentials = c
	case "business-output":
		var c businessConfig
		if json.Unmarshal(defaults, &c) != nil {
			return errJSON
		}
		if replacesRules {
			c.Rules = nil
		}
		if strict(ctx, b, &c) != nil || c.Version != 1 || !positive(c.Threshold) || len(c.Rules) == 0 || len(c.Rules) > maxRules {
			return errJSON
		}
		seen := map[string]bool{}
		for i := range c.Rules {
			r := &c.Rules[i]
			if !identifier.MatchString(r.ID) || !identifier.MatchString(r.Version) || seen[r.ID] || !positive(r.Weight) || len(r.Keywords) == 0 || len(r.Keywords) > 32 {
				return errJSON
			}
			seen[r.ID] = true
			words := map[string]bool{}
			for j, k := range r.Keywords {
				k = strings.ToLower(k)
				if strings.TrimSpace(k) == "" || len(k) > 128 || words[k] {
					return errJSON
				}
				words[k] = true
				r.Keywords[j] = k
			}
		}
		s.business = c
	case "pricing-sync":
		var c pricingConfig
		if json.Unmarshal(defaults, &c) != nil {
			return errJSON
		}
		if replacesRules {
			c.Rules = nil
		}
		if strict(ctx, b, &c) != nil || c.Version != 1 || len(c.Rules) == 0 || len(c.Rules) > maxRules || len(c.SourceURL) > 512 {
			return errJSON
		}
		u, e := url.Parse(c.SourceURL)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return errJSON
		}
		seen := map[[2]int64]map[string]bool{}
		for _, r := range c.Rules {
			k := [2]int64{r.ChannelID, r.PricingID}
			if seen[k] == nil {
				seen[k] = map[string]bool{}
			}
			if r.ChannelID <= 0 || r.PricingID <= 0 || !sourceKey.MatchString(r.SourceKey) || !pricingFields[r.Field] || seen[k][r.Field] || !positive(r.Multiplier) || !positive(r.MaxPrice) || r.MaxChangeRatio == nil || *r.MaxChangeRatio < 0 || *r.MaxChangeRatio > 100 {
				return errJSON
			}
			seen[k][r.Field] = true
		}
		s.pricing = c
	}
	return ctx.Err()
}
