package acls

import (
	"errors"

	"go.aporeto.io/trireme-lib/policy"
)

var catchAllPolicy = &policy.FlowPolicy{Action: policy.Reject, PolicyID: "default", ServiceID: "default"}

// ACLCache holds all the ACLS in an internal DB
// map[prefixes][subnets] -> list of ports with their actions
type ACLCache struct {
	reject  *acl
	accept  *acl
	observe *acl
}

type prefixRules struct {
	mask  uint32
	rules map[uint32]portActionList
}

// NewACLCache creates a new ACL cache
func NewACLCache() *ACLCache {
	return &ACLCache{
		reject:  newACL(),
		accept:  newACL(),
		observe: newACL(),
	}
}

// AddRule adds a single rule to the ACL Cache
func (c *ACLCache) AddRule(rule policy.IPRule) (err error) {
	defer func() {
		c.reject.reverseSort()
		c.accept.reverseSort()
		c.observe.reverseSort()
	}()
	
	if rule.Policy.ObserveAction.ObserveApply() {
		return c.observe.addRule(rule)
	}

	if rule.Policy.Action.Accepted() {
		return c.accept.addRule(rule)
	}

	return c.reject.addRule(rule)
}

// AddRuleList adds a list of rules to the cache
func (c *ACLCache) AddRuleList(rules policy.IPRuleList) (err error) {

	for _, rule := range rules {
		if err = c.AddRule(rule); err != nil {
			return
		}
	}

	c.reject.reverseSort()
	c.accept.reverseSort()
	c.observe.reverseSort()
	return
}

// GetMatchingAction gets the matching action
func (c *ACLCache) GetMatchingAction(ip []byte, port uint16) (report *policy.FlowPolicy, packet *policy.FlowPolicy, err error) {
	report, packet, err = c.reject.getMatchingAction(ip, port, report)
	if err == nil {
		return
	}

	report, packet, err = c.accept.getMatchingAction(ip, port, report)
	if err == nil {
		return
	}

	report, packet, err = c.observe.getMatchingAction(ip, port, report)
	if err == nil {
		return
	}

	if report == nil {
		report = catchAllPolicy
	}

	if packet == nil {
		packet = catchAllPolicy
	}

	return report, packet, errors.New("no match")
}
