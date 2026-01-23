package workspace

import "log"

// getMergedConfig extracts and merges configuration hierarchy
func (tg *TaskGenerator) getMergedConfig(config map[string]any) map[string]any {
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

	return tg.mergeConfigs(
		globalConfig,
		platforms[tg.Platform],
		architectures[tg.Architecture],
		configurations[tg.Configuration],
	)
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

		log.Printf("Applied platform '%s' overrides to target '%s'", tg.Platform, name)
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

		case "include_dirs", "libs", "packages", "depends_on":
			// These could be merged or replaced - for now, replace if present
			result[k] = v

		default:
			// For other keys, override takes precedence
			result[k] = v
		}
	}

	return result
}
