package lookup

import (
	"testing"

	"go.aporeto.io/trireme-lib/policy"

	. "github.com/smartystreets/goconvey/convey"
)

var (
	appEqWeb = policy.KeyValueOperator{
		Key:      "app",
		Value:    []string{"web"},
		Operator: policy.Equal,
		ID:       "1",
	}
	envEqDemo = policy.KeyValueOperator{
		Key:      "env",
		Value:    []string{"demo"},
		Operator: policy.Equal,
		ID:       "2",
	}

	envEqDemoOrQa = policy.KeyValueOperator{
		Key:      "env",
		Value:    []string{"demo", "qa"},
		Operator: policy.Equal,
		ID:       "3",
	}

	dcKeyExists = policy.KeyValueOperator{
		Key:      "dc",
		Operator: policy.KeyExists,
	}

	langNotJava = policy.KeyValueOperator{
		Key:      "lang",
		Value:    []string{"java"},
		Operator: policy.NotEqual,
	}

	envNotDemoOrQA = policy.KeyValueOperator{
		Key:      "env",
		Value:    []string{"demo", "qa"},
		Operator: policy.NotEqual,
	}

	envKeyNotExists = policy.KeyValueOperator{
		Key:      "env",
		Operator: policy.KeyNotExists,
	}

	vulnerKey = policy.KeyValueOperator{
		Key:      "vulnerability",
		Value:    []string{"high"},
		Operator: policy.Equal,
	}

	vulnerLowKey = policy.KeyValueOperator{
		Key:      "vulnerability",
		Value:    []string{"low"},
		Operator: policy.Equal,
	}

	namespaceKey = policy.KeyValueOperator{
		Key:      "namespace",
		Value:    []string{"/a/b/*"},
		Operator: policy.Equal,
	}

	appEqWebAndenvEqDemo = policy.TagSelector{
		Clause: []policy.KeyValueOperator{appEqWeb, envEqDemo},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}

	appEqWebAndEnvEqDemoOrQa = policy.TagSelector{
		Clause: []policy.KeyValueOperator{appEqWeb, envEqDemoOrQa},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}

	dcTagExists = policy.TagSelector{
		Clause: []policy.KeyValueOperator{dcKeyExists},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}

	policylangNotJava = policy.TagSelector{
		Clause: []policy.KeyValueOperator{langNotJava},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}

	appEqWebAndenvNotDemoOrQA = policy.TagSelector{
		Clause: []policy.KeyValueOperator{appEqWeb, envNotDemoOrQA},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}

	envKeyNotExistsAndAppEqWeb = policy.TagSelector{
		Clause: []policy.KeyValueOperator{envKeyNotExists, appEqWeb},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}

	vulnTagPolicy = policy.TagSelector{
		Clause: []policy.KeyValueOperator{vulnerKey},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}

	policyNamespace = policy.TagSelector{
		Clause: []policy.KeyValueOperator{namespaceKey, vulnerLowKey},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}

	domainParent = policy.KeyValueOperator{
		Key:      "domain",
		Value:    []string{"com.example.*", "com.*", "com.longexample.*", "com.ex.*"},
		Operator: policy.Equal,
	}

	domainFull = policy.KeyValueOperator{
		Key:      "domain",
		Value:    []string{"com.example.web"},
		Operator: policy.Equal,
	}

	policyDomainParent = policy.TagSelector{
		Clause: []policy.KeyValueOperator{domainParent},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}

	policyDomainFull = policy.TagSelector{
		Clause: []policy.KeyValueOperator{domainFull},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}

	policyEnvDoesNotExist = policy.TagSelector{
		Clause: []policy.KeyValueOperator{envKeyNotExists},
		Policy: &policy.FlowPolicy{Action: policy.Accept},
	}
)

// TestConstructorNewPolicyDB tests the NewPolicyDB constructor
func TestConstructorNewPolicyDB(t *testing.T) {
	Convey("Given that I instantiate a new policy DB, I should not get nil", t, func() {

		p := &PolicyDB{}

		policyDB := NewPolicyDB()

		So(policyDB, ShouldHaveSameTypeAs, p)
	})
}

// TestFuncAddPolicy tests the add policy function
func TestFuncAddPolicy(t *testing.T) {

	Convey("Given an empty policy DB", t, func() {
		policyDB := NewPolicyDB()

		Convey("When I add a single policy it should be associated with all the tags", func() {
			index := policyDB.AddPolicy(appEqWebAndenvEqDemo)

			So(policyDB.numberOfPolicies, ShouldEqual, 1)
			So(index, ShouldEqual, 1)
			for _, c := range appEqWebAndenvEqDemo.Clause {
				So(policyDB.equalMapTable[c.Key][c.Value[0]], ShouldNotBeNil)
				So(policyDB.equalMapTable[c.Key][c.Value[0]][0].index, ShouldEqual, index)
				So(policyDB.equalPrefixes[c.Key], ShouldNotContain, c.Key)
			}
		})

		Convey("When I add a policy with the not equal operator, it should be added to the notEqual db", func() {
			index := policyDB.AddPolicy(policylangNotJava)

			So(policyDB.numberOfPolicies, ShouldEqual, 1)
			So(index, ShouldEqual, 1)
			for _, c := range policylangNotJava.Clause {
				So(policyDB.notEqualMapTable[c.Key][c.Value[0]], ShouldNotBeNil)
				So(policyDB.notEqualMapTable[c.Key][c.Value[0]][0].index, ShouldEqual, index)
				So(policyDB.equalPrefixes, ShouldNotContainKey, c.Key)
			}
		})

		Convey("When I add a policy with the KeyExists operator, it should be added as a prefix of 0", func() {
			index := policyDB.AddPolicy(dcTagExists)

			key := dcTagExists.Clause[0].Key
			So(policyDB.numberOfPolicies, ShouldEqual, 1)
			So(index, ShouldEqual, 1)
			So(policyDB.equalPrefixes, ShouldContainKey, key)
			So(policyDB.equalPrefixes[key], ShouldContain, 0)
			So(policyDB.equalMapTable[key], ShouldHaveLength, 1)
			So(policyDB.equalMapTable[key], ShouldContainKey, "")
			So(policyDB.equalPrefixes[key], ShouldHaveLength, 1)
		})

		Convey("When I add a policy with prefixes, it should be associated with the right prefixes", func() {
			index := policyDB.AddPolicy(policyDomainParent)

			key := policyDomainParent.Clause[0].Key
			value0 := policyDomainParent.Clause[0].Value[0]
			value1 := policyDomainParent.Clause[0].Value[1]
			value2 := policyDomainParent.Clause[0].Value[2]
			value3 := policyDomainParent.Clause[0].Value[3]
			So(policyDB.numberOfPolicies, ShouldEqual, 1)
			So(index, ShouldEqual, 1)
			So(policyDB.equalMapTable[key], ShouldHaveLength, 4)
			So(policyDB.equalMapTable[key], ShouldContainKey, value0[:len(value0)-1])
			So(policyDB.equalMapTable[key], ShouldContainKey, value1[:len(value1)-1])
			So(policyDB.equalMapTable[key], ShouldContainKey, value2[:len(value2)-1])
			So(policyDB.equalMapTable[key], ShouldContainKey, value3[:len(value3)-1])
			So(policyDB.equalPrefixes[key], ShouldHaveLength, 4)
			So(policyDB.equalPrefixes[key], ShouldContain, len(value0)-1)
			So(policyDB.equalPrefixes[key], ShouldContain, len(value1)-1)
			So(policyDB.equalPrefixes[key], ShouldContain, len(value2)-1)
			So(policyDB.equalPrefixes[key], ShouldContain, len(value3)-1)
		})

	})
}

// TestFuncSearch tests the search function of the lookup
func TestFuncSearch(t *testing.T) {
	// policy1 : app=web and env=demo
	// policy2 : lang != java
	// policy3 : dc=*
	// policy4: app=web and env IN (demo, qa)
	// policy5: app=web and env NotIN (demo, qa)
	// policy6: app=web not env=*
	// policy7: domain IN ("com.*", "com.example.*")
	// policy8: domain=com.example.web
	// policy9: env doesn't exist
	// policy10: vulnerability=high
	// policy11: namespace=/a/b/* and vulnerability=low

	Convey("Given an empty policyDB", t, func() {
		policyDB := NewPolicyDB()
		Convey("Given that I add two policy rules", func() {
			index1 := policyDB.AddPolicy(appEqWebAndenvEqDemo)
			index2 := policyDB.AddPolicy(policylangNotJava)
			index3 := policyDB.AddPolicy(dcTagExists)
			index4 := policyDB.AddPolicy(appEqWebAndEnvEqDemoOrQa)
			index5 := policyDB.AddPolicy(appEqWebAndenvNotDemoOrQA)
			index6 := policyDB.AddPolicy(envKeyNotExistsAndAppEqWeb)
			index7 := policyDB.AddPolicy(policyDomainParent)
			index8 := policyDB.AddPolicy(policyDomainFull)
			index9 := policyDB.AddPolicy(policyEnvDoesNotExist)
			index10 := policyDB.AddPolicy(vulnTagPolicy)
			index11 := policyDB.AddPolicy(policyNamespace)

			So(index1, ShouldEqual, 1)
			So(index2, ShouldEqual, 2)
			So(index3, ShouldEqual, 3)
			So(index4, ShouldEqual, 4)
			So(index5, ShouldEqual, 5)
			So(index6, ShouldEqual, 6)
			So(index9, ShouldEqual, 9)

			Convey("The vulnerability tag policy should match", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("vulnerability", "high")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, index10)
				So(action.(*policy.FlowPolicy).Action, ShouldEqual, policy.Accept)
			})

			Convey("A policy that matches ID should match", func() {
				tags := policy.NewTagStoreFromSlice([]string{"1"})
				index, action := policyDB.Search(tags)
				So(action, ShouldNotBeNil)
				So(index, ShouldEqual, index6)
			})

			Convey("A policy that matches only the namespace, shoud not match", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("namespace", "/a/b/c/d")
				tags.AppendKeyValue("env", "privatedemo")

				index, action := policyDB.Search(tags)
				So(action, ShouldBeNil)
				So(index, ShouldEqual, -1)
			})

			Convey("A policy that matches namespace and vulnerability low, shoud match", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("namespace", "/a/b/c/d")
				tags.AppendKeyValue("vulnerability", "low")
				tags.AppendKeyValue("env", "privatedemo")

				index, action := policyDB.Search(tags)
				So(action, ShouldNotBeNil)
				So(index, ShouldEqual, index11)
			})

			Convey("Given that I search for a single matching that matches the equal rules, it should return the correct index,", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("app", "web")
				tags.AppendKeyValue("env", "demo")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, index1)
				So(action.(*policy.FlowPolicy).Action, ShouldEqual, policy.Accept)
			})

			Convey("Given that I search for a single matching that matches the not equal rules, it should return the right index,", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("lang", "go")
				tags.AppendKeyValue("env", "demo")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, index2)
				So(action.(*policy.FlowPolicy).Action, ShouldEqual, policy.Accept)
			})

			Convey("Given that I search for rules that match the KeyExists Policy, it should return the right index  ", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("dc", "EAST")
				tags.AppendKeyValue("env", "demo")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, index3)
				So(action.(*policy.FlowPolicy).Action, ShouldEqual, policy.Accept)
			})

			Convey("Given that I search for a single matching that matches the Or rules, it should return the right index,", func() {

				tags := policy.NewTagStore()
				tags.AppendKeyValue("app", "web")
				tags.AppendKeyValue("env", "qa")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, index4)
				So(action.(*policy.FlowPolicy).Action, ShouldEqual, policy.Accept)
			})

			Convey("Given that I search for a single matching that matches the NOT Or rlues, it should return the right index,", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("app", "web")
				tags.AppendKeyValue("env", "prod")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, index5)
				So(action.(*policy.FlowPolicy).Action, ShouldEqual, policy.Accept)
			})

			Convey("Given that I search for a single clause  that fails in the Not OR operator, it should fail ,", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("lang", "java")
				tags.AppendKeyValue("env", "demo")
				tags.AppendKeyValue("app", "db")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, -1)
				So(action, ShouldBeNil)
			})

			Convey("Given that I search for rules that do not match, it should return an error ", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("tag", "node")
				tags.AppendKeyValue("env", "node")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, -1)
				So(action, ShouldBeNil)
			})

			Convey("Given that I search for a single that succeeds in the Not Key  operator, it should succeed ,", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("app", "web")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, index6)
				So(action.(*policy.FlowPolicy).Action, ShouldEqual, policy.Accept)
			})

			Convey("Given that I search for a value that matches a prefix", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("domain", "com.example.db")
				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, index7)
				So(action.(*policy.FlowPolicy).Action, ShouldEqual, policy.Accept)
			})

			Convey("Given that I search for a value that matches a complete value ", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("domain", "com.example.web")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, index8)
				So(action.(*policy.FlowPolicy).Action, ShouldEqual, policy.Accept)
			})

			Convey("Given that I search for a value that matches some of the prefix, it should return err  ", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("domain", "co")
				tags.AppendKeyValue("env", "node")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, -1)
				So(action, ShouldBeNil)
			})

			Convey("Given that I search for a value matches only the env not exists policy ", func() {
				tags := policy.NewTagStore()
				tags.AppendKeyValue("sometag", "nomatch")

				index, action := policyDB.Search(tags)
				So(index, ShouldEqual, index9)
				So(action.(*policy.FlowPolicy).Action, ShouldEqual, policy.Accept)
			})

		})

	})
}

// TestFuncDumbDB is a mock test for the print function
func TestFuncDumpDB(t *testing.T) {
	Convey("Given an empty policy DB", t, func() {
		policyDB := NewPolicyDB()

		Convey("Given that I add two policy rules, I should be able to print the db ", func() {
			index1 := policyDB.AddPolicy(appEqWebAndenvEqDemo)
			index2 := policyDB.AddPolicy(policylangNotJava)
			So(index1, ShouldEqual, 1)
			So(index2, ShouldEqual, 2)

			policyDB.PrintPolicyDB()

		})
	})
}
