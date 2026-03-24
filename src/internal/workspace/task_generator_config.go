package workspace

import (
	"fmt"
	"sort"
	"strings"

	"buildy/pkg/util"
)

// getMergedConfig extracts and merges configuration hierarchy, then resolves
// abstract keywords via toolchain flag_mappings and applies flag removals.
func (tg *TaskGenerator) getMergedConfig(config map[string]any) (map[string]any, error) {
	globalConfig := make(map[string]any)
	if gc, ok := config["config"].(map[string]any); ok {
		globalConfig = gc
	}

	platforms := make(map[string]any)
	if p, ok := config["platforms"].(map[string]any); ok {
		platforms = p
	}

	architectures := make(map[string]any)
	if a, ok := config["architectures"].(map[string]any); ok {
		architectures = a
	}

	configurations := make(map[string]any)
	if c, ok := config["configurations"].(map[string]any); ok {
		configurations = c
	}

	// Also extract from environment section (new DSL format)
	if env, ok := config["environment"].(map[string]any); ok {
		if compile, ok := env["compile"].(map[string]any); ok {
			for k, v := range compile {
				globalConfig[k] = v
			}
		}
		if link, ok := env["link"].(map[string]any); ok {
			for k, v := range link {
				globalConfig["link_"+k] = v
			}
		}
		if envConfigs, ok := env["configurations"].(map[string]any); ok {
			for configName, configData := range envConfigs {
				if configMap, ok := configData.(map[string]any); ok {
					if _, exists := configurations[configName]; !exists {
						configurations[configName] = configMap
					} else if existingMap, ok := configurations[configName].(map[string]any); ok {
						for k, v := range configMap {
							existingMap[k] = v
						}
					}
				}
			}
		}
	}

	// Get the configuration for the current build configuration (debug/release)
	configSettings := configurations[tg.Configuration]

	// Extract platform-specific settings from within the configuration
	var configPlatformSettings any
	if configMap, ok := configSettings.(map[string]any); ok {
		if platformSettings, ok := configMap[tg.Platform]; ok {
			configPlatformSettings = platformSettings
		}
	}

	merged := tg.mergeConfigs(
		globalConfig,
		platforms[tg.Platform],
		architectures[tg.Architecture],
		configSettings,
		configPlatformSettings,
	)

	// Resolve filtered lists at the environment level so platform/arch/config/
	// toolchain filters work inside environment and configuration sections.
	ctx := tg.getBuildContext()
	for key, val := range merged {
		if _, ok := val.([]any); ok {
			merged[key] = ResolveFilteredList(val, ctx)
		}
	}

	// Resolve abstract keywords (optimization, warnings, symbols, runtime, etc.)
	// via the toolchain's flag_mappings. Resolved flags are prepended to merged["flags"].
	if tg.CurrentToolchain != nil && len(tg.CurrentToolchain.FlagMappings) > 0 {
		if err := resolveAbstractKeywords(merged, tg.CurrentToolchain.FlagMappings); err != nil {
			return nil, err
		}
	}

	// Apply configuration-level flag/define removals
	if removals := ExtractStringList(merged["remove_flags"]); len(removals) > 0 {
		merged["flags"] = applyFlagRemovals(ExtractStringList(merged["flags"]), removals)
		delete(merged, "remove_flags")
	}
	if removals := ExtractStringList(merged["remove_defines"]); len(removals) > 0 {
		merged["defines"] = applyFlagRemovals(ExtractStringList(merged["defines"]), removals)
		delete(merged, "remove_defines")
	}
	if removals := ExtractStringList(merged["remove_link_flags"]); len(removals) > 0 {
		merged["link_flags"] = applyFlagRemovals(ExtractStringList(merged["link_flags"]), removals)
		delete(merged, "remove_link_flags")
	}

	return tg.resolveConfigMap(merged), nil
}

// resolveAbstractKeywords iterates over the toolchain's flag_mappings and,
// for each keyword present in merged, resolves the user's value to concrete
// flags. Resolved flags are prepended to merged["flags"]. The consumed
// keyword key is deleted from merged. Returns an error if a keyword's value
// cannot be resolved (unknown value for that toolchain).
func resolveAbstractKeywords(merged map[string]any, mappings map[string]map[string][]string) error {
	var resolvedFlags []string

	// Sort keywords for deterministic flag ordering across builds.
	sortedKeywords := make([]string, 0, len(mappings))
	for keyword := range mappings {
		sortedKeywords = append(sortedKeywords, keyword)
	}
	sort.Strings(sortedKeywords)

	for _, keyword := range sortedKeywords {
		valueMap := mappings[keyword]
		rawVal, exists := merged[keyword]
		if !exists {
			continue
		}

		userValue := ""
		switch v := rawVal.(type) {
		case string:
			userValue = v
		case bool:
			userValue = fmt.Sprintf("%t", v)
		default:
			continue
		}

		flags, valid := valueMap[userValue]
		if !valid {
			validValues := make([]string, 0, len(valueMap))
			for k := range valueMap {
				validValues = append(validValues, k)
			}
			sort.Strings(validValues)
			return fmt.Errorf("abstract keyword %q has unresolvable value %q; valid values: [%s]",
				keyword, userValue, strings.Join(validValues, ", "))
		}

		resolvedFlags = append(resolvedFlags, flags...)
		delete(merged, keyword)
	}

	if len(resolvedFlags) > 0 {
		existing := ExtractStringList(merged["flags"])
		merged["flags"] = append(resolvedFlags, existing...)
	}

	return nil
}

// applyFlagRemovals returns base with all entries in removals excluded.
func applyFlagRemovals(base []string, removals []string) []string {
	removeSet := make(map[string]bool, len(removals))
	for _, f := range removals {
		removeSet[f] = true
	}
	result := make([]string, 0, len(base))
	for _, f := range base {
		if !removeSet[f] {
			result = append(result, f)
		}
	}
	return result
}

// mergeConfigs merges configuration hierarchy
func (tg *TaskGenerator) mergeConfigs(configs ...any) map[string]any {
	merged := make(map[string]any)
	for _, configRaw := range configs {
		config, ok := configRaw.(map[string]any)
		if !ok {
			continue
		}
		for key, value := range config {
			if valueList, ok := value.([]any); ok {
				if existingList, ok := merged[key].([]any); ok {
					merged[key] = append(existingList, valueList...)
				} else {
					merged[key] = value
				}
			} else {
				merged[key] = value
			}
		}
	}
	return merged
}

// applyPlatformTargetOverrides merges platform-specific target configurations
// with the base targets. For example, if platforms.linux.targets.libraries defines
// overrides for a library, those are merged with the base library config.
func (tg *TaskGenerator) applyPlatformTargetOverrides(config map[string]any, baseTargets []any, targetType string) []any {
	// Get platform-specific targets
	platforms, ok := config["platforms"].(map[string]any)
	if !ok {
		return baseTargets
	}

	platformConfig, ok := platforms[tg.Platform].(map[string]any)
	if !ok {
		return baseTargets
	}

	platformTargets, ok := platformConfig["targets"].(map[string]any)
	if !ok {
		return baseTargets
	}

	platformTargetList, ok := platformTargets[targetType].([]any)
	if !ok || len(platformTargetList) == 0 {
		return baseTargets
	}

	// Build a map of platform overrides by target name
	overridesByName := make(map[string]map[string]any)
	for _, overrideRaw := range platformTargetList {
		override, ok := overrideRaw.(map[string]any)
		if !ok {
			continue
		}
		name, ok := override["name"].(string)
		if !ok {
			continue
		}
		overridesByName[name] = override
	}

	// Merge overrides into base targets
	result := make([]any, 0, len(baseTargets))
	for _, baseRaw := range baseTargets {
		base, ok := baseRaw.(map[string]any)
		if !ok {
			result = append(result, baseRaw)
			continue
		}

		name, ok := base["name"].(string)
		if !ok {
			result = append(result, baseRaw)
			continue
		}

		override, hasOverride := overridesByName[name]
		if !hasOverride {
			result = append(result, base)
			continue
		}

		// Merge override into base (deep merge for special keys)
		merged := tg.mergeTargetConfig(base, override)
		result = append(result, merged)

		util.LogInfo("Applied platform '%s' overrides to target '%s'", tg.Platform, name)
	}

	return result
}

// mergeTargetConfig merges a platform-specific target override into the base target config
func (tg *TaskGenerator) mergeTargetConfig(base, override map[string]any) map[string]any {
	result := make(map[string]any)

	// Copy base values
	for k, v := range base {
		result[k] = v
	}

	// Apply overrides
	for k, v := range override {
		if k == "name" {
			continue // Don't override name
		}

		switch k {
		case "sources":
			// Sources: replace entirely with platform-specific sources
			result[k] = v

		case "compile":
			// Compile settings: deep merge
			if baseCompile, ok := base["compile"].(map[string]any); ok {
				if overrideCompile, ok := v.(map[string]any); ok {
					mergedCompile := make(map[string]any)
					for ck, cv := range baseCompile {
						mergedCompile[ck] = cv
					}
					for ck, cv := range overrideCompile {
						// For defines, append rather than replace
						if ck == "defines" {
							if baseDefines, ok := mergedCompile["defines"].([]any); ok {
								if overrideDefines, ok := cv.([]any); ok {
									mergedCompile["defines"] = append(baseDefines, overrideDefines...)
								}
							} else {
								mergedCompile[ck] = cv
							}
						} else {
							mergedCompile[ck] = cv
						}
					}
					result[k] = mergedCompile
				} else {
					result[k] = v
				}
			} else {
				result[k] = v
			}

		case "link":
			// Link settings: deep merge (same pattern as compile)
			if baseLink, ok := base["link"].(map[string]any); ok {
				if overrideLink, ok := v.(map[string]any); ok {
					mergedLink := make(map[string]any)
					for lk, lv := range baseLink {
						mergedLink[lk] = lv
					}
					for lk, lv := range overrideLink {
						if lk == "flags" {
							if baseFlags, ok := mergedLink["flags"].([]any); ok {
								if overrideFlags, ok := lv.([]any); ok {
									mergedLink["flags"] = append(baseFlags, overrideFlags...)
								}
							} else {
								mergedLink[lk] = lv
							}
						} else {
							mergedLink[lk] = lv
						}
					}
					result[k] = mergedLink
				} else {
					result[k] = v
				}
			} else {
				result[k] = v
			}

		case "include_dirs", "libs", "deps", "depends_on":
			// These could be merged or replaced - for now, replace if present
			result[k] = v

		default:
			// For other keys, override takes precedence
			result[k] = v
		}
	}

	return result
}
