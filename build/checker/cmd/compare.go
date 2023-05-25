package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/falcosecurity/testing/pkg/falco"
	"github.com/falcosecurity/testing/pkg/run"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type falcoCompareOutput struct {
	Lists []struct {
		Details struct {
			Lists []string `json:"lists"`
		} `json:"details"`
		Info struct {
			Items []string `json:"items"`
			Name  string   `json:"name"`
		} `json:"info"`
	} `json:"lists"`
	Macros []struct {
		Details struct {
			ConditionFields []string `json:"condition_fields"`
			Events          []string `json:"events"`
			Lists           []string `json:"lists"`
			Macros          []string `json:"macros"`
			Operators       []string `json:"operators"`
		} `json:"details"`
		Info struct {
			Condition string `json:"condition"`
			Name      string `json:"name"`
		} `json:"info"`
	} `json:"macros"`
	Rules []struct {
		Details struct {
			ConditionFields    []string `json:"condition_fields"`
			Events             []string `json:"events"`
			ExceptionFields    []string `json:"exception_fields"`
			ExceptionOperators []string `json:"exception_operators"`
			Lists              []string `json:"lists"`
			Macros             []string `json:"macros"`
			Operators          []string `json:"operators"`
			OutputFields       []string `json:"output_fields"`
		} `json:"details"`
		Info struct {
			Condition   string   `json:"condition"`
			Description string   `json:"description"`
			Enabled     bool     `json:"enabled"`
			Name        string   `json:"name"`
			Output      string   `json:"output"`
			Priority    string   `json:"priority"`
			Source      string   `json:"source"`
			Tags        []string `json:"tags"`
		} `json:"info"`
	} `json:"rules"`
}

func (f *falcoCompareOutput) ListNames() []string {
	var names []string
	for _, l := range f.Lists {
		names = append(names, l.Info.Name)
	}
	return names
}

func (f *falcoCompareOutput) MacroNames() []string {
	var names []string
	for _, l := range f.Macros {
		names = append(names, l.Info.Name)
	}
	return names
}

func (f *falcoCompareOutput) RuleNames() []string {
	var names []string
	for _, l := range f.Rules {
		names = append(names, l.Info.Name)
	}
	return names
}

func getCompareOutput(falcoImage, ruleFile string) (*falcoCompareOutput, error) {
	// run falco and collect/print validation issues
	runner, err := run.NewDockerRunner(falcoImage, defaultFalcoDockerEntrypoint, nil)
	if err != nil {
		return nil, err
	}

	// todo(jasondellaluce): we need to resolve plugin dependencies by
	//   - running falcoctl before
	//   - crafting a plugin config that loads the required plugins
	res := falco.Test(
		runner,
		falco.WithRules(run.NewLocalFileAccessor(ruleFile, ruleFile)),
		falco.WithOutputJSON(),
		falco.WithArgs("-L"),
	)

	// collect errors
	err = errAppend(err, res.Err())
	if res.ExitCode() != 0 {
		err = errAppend(err, fmt.Errorf("unexpected exit code (%d)", res.ExitCode()))
	}
	if err != nil {
		logrus.Info(res.Stderr())
		return nil, err
	}

	// unmarshal json output
	var out falcoCompareOutput
	err = json.Unmarshal(([]byte)(res.Stdout()), &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func strSliceToMap(s []string) map[string]bool {
	items := make(map[string]bool)
	for _, item := range s {
		items[item] = true
	}
	return items
}

func diffStrSet(left, right []string) map[string]bool {
	l := strSliceToMap(left)
	r := strSliceToMap(right)
	for k := range r {
		delete(l, k)
	}
	return l
}

// compareStrSets returns -1 if "left" has at least one item not contained in "right",
// 1 if "right" has at least one item not contained in "left", and 0 otherwise.
func compareStrSets(left, right []string) int {
	l := strSliceToMap(left)
	r := strSliceToMap(right)
	for k := range l {
		if _, ok := r[k]; !ok {
			return -1
		}
	}
	for k := range r {
		if _, ok := l[k]; !ok {
			return -1
		}
	}
	return 0
}

func compareRulesPatch(left, right *falcoCompareOutput) (res []string) {
	for _, l := range left.Rules {
		for _, r := range right.Rules {
			if l.Info.Name == r.Info.Name {
				// Enabling at default one or more rules that used to be disabled
				if !l.Info.Enabled && r.Info.Enabled {
					res = append(res, fmt.Sprintf("rule '%s' has been enabled by default", l.Info.Name))
				}

				// Matching more events in a rule condition
				if len(diffStrSet(r.Details.Events, l.Details.Events)) > 0 {
					res = append(res, fmt.Sprintf("rule '%s' matches more events than before", l.Info.Name))
				}

				// A rule has different output fields
				if compareInt(len(l.Details.OutputFields), len(r.Details.OutputFields)) != 0 {
					res = append(res, fmt.Sprintf("rule '%s' changed its output fields", l.Info.Name))
				}

				// A rule has more tags than before
				if len(diffStrSet(r.Info.Tags, l.Info.Tags)) > 0 {
					res = append(res, fmt.Sprintf("rule '%s' has more tags than before", l.Info.Name))
				}

				// a priority becomes more urgent than before
				if compareFalcoPriorities(r.Info.Priority, l.Info.Priority) > 0 {
					res = append(res, fmt.Sprintf("rule '%s' has a more urgent priority than before", l.Info.Name))
				}

				// todo: Adding or removing exceptions for one or more Falco rules
				// todo: add required engine version to Falco outputs
				// todo: add exception names to Falco outputs
			}
		}
	}

	for _, l := range left.Lists {
		for _, r := range right.Lists {
			if l.Info.Name == r.Info.Name {
				// Adding or removing items for one or more lists
				if compareStrSets(l.Info.Items, r.Info.Items) != 0 {
					res = append(res, fmt.Sprintf("list '%s' has some item added or removed", l.Info.Name))
				}
			}
		}
	}

	for _, l := range left.Macros {
		for _, r := range right.Macros {
			if l.Info.Name == r.Info.Name {
				// Matching more events in a macro condition
				if len(diffStrSet(r.Details.Events, l.Details.Events)) > 0 {
					res = append(res, fmt.Sprintf("macro '%s' matches more events than before", l.Info.Name))
				}
			}
		}
	}

	return
}

func compareRulesMinor(left, right *falcoCompareOutput) (res []string) {
	// todo: Incrementing the required_engine_version number
	// todo: Incrementing the required_plugin_versions version requirement for one or more plugin
	// todo: Adding a new plugin version requirement in required_plugin_versions

	// Adding one or more lists, macros, or rules
	if compareStrSets(left.RuleNames(), right.RuleNames()) > 0 {
		res = append(res, "some rule has been added")
	}
	if compareStrSets(left.MacroNames(), right.MacroNames()) > 0 {
		res = append(res, "some macro has been added")
	}
	if compareStrSets(left.ListNames(), right.ListNames()) > 0 {
		res = append(res, "some list has been added")
	}
	return
}

func compareRulesMajor(left, right *falcoCompareOutput) (res []string) {
	// Renaming or removing a list, macro, or rule
	if compareStrSets(left.RuleNames(), right.RuleNames()) < 0 {
		res = append(res, "some rule has been removed")
	}
	if compareStrSets(left.MacroNames(), right.MacroNames()) < 0 {
		res = append(res, "some macro has been removed")
	}
	if compareStrSets(left.ListNames(), right.ListNames()) < 0 {
		res = append(res, "some list has been removed")
	}

	for _, l := range left.Rules {
		for _, r := range right.Rules {
			if l.Info.Name == r.Info.Name {
				// Rule has a different source
				if l.Info.Source != r.Info.Source {
					res = append(res, fmt.Sprintf("rule '%s' has different source (before='%s', after='%s')", l.Info.Name, l.Info.Source, r.Info.Source))
				}

				// Disabling at default one or more rules that used to be enabled
				if l.Info.Enabled && !r.Info.Enabled {
					res = append(res, fmt.Sprintf("rule '%s' has been disabled by default", l.Info.Name))
				}

				// Matching less events in a rule condition
				if len(diffStrSet(l.Details.Events, r.Details.Events)) > 0 {
					res = append(res, fmt.Sprintf("rule '%s' matches less events than before", l.Info.Name))
				}

				// A rule has less tags than before
				if len(diffStrSet(l.Info.Tags, r.Info.Tags)) > 0 {
					res = append(res, fmt.Sprintf("rule '%s' has less tags than before", l.Info.Name))
				}

				// a priority becomes less urgent than before
				if compareFalcoPriorities(l.Info.Priority, r.Info.Priority) > 0 {
					res = append(res, fmt.Sprintf("rule '%s' has a less urgent priority than before", l.Info.Name))
				}
			}
		}
	}

	for _, l := range left.Macros {
		for _, r := range right.Macros {
			if l.Info.Name == r.Info.Name {
				// Matching different events in a macro condition
				if len(diffStrSet(l.Details.Events, r.Details.Events)) > 0 ||
					len(diffStrSet(r.Details.Events, l.Details.Events)) > 0 {
					res = append(res, fmt.Sprintf("macro '%s' matches different events than before", l.Info.Name))
				}
			}
		}
	}
	return
}

var compareCmd = &cobra.Command{
	Use: "compare",
	// todo: load more than one rules files both on left and right
	Short: "Compare two rules files and suggest version changes",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) < 2 {
			return fmt.Errorf("you must specify at least two rules file")
		}

		falcoImage, err := cmd.Flags().GetString("falco-image")
		if err != nil {
			return err
		}

		leftOutput, err := getCompareOutput(falcoImage, args[0])
		if err != nil {
			return err
		}

		rightOutput, err := getCompareOutput(falcoImage, args[1])
		if err != nil {
			return err
		}

		diff := compareRulesMajor(leftOutput, rightOutput)
		if len(diff) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Major version change detected for the following reasons:")
			for _, s := range diff {
				fmt.Fprintln(cmd.OutOrStdout(), "* "+s)
			}
			return nil
		}

		diff = compareRulesMinor(leftOutput, rightOutput)
		if len(diff) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Minor version change detected for the following reasons:")
			for _, s := range diff {
				fmt.Fprintln(cmd.OutOrStdout(), "* "+s)
			}
			return nil
		}

		diff = compareRulesPatch(leftOutput, rightOutput)
		if len(diff) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Patch version change detected for the following reasons:")
			for _, s := range diff {
				fmt.Fprintln(cmd.OutOrStdout(), "* "+s)
			}
			return nil
		}

		return nil
	},
}

func init() {
	compareCmd.Flags().StringP("falco-image", "i", defaultFalcoDockerImage, "Docker image of Falco to be used for validation")
	rootCmd.AddCommand(compareCmd)
}