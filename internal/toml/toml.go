// Package toml 提供一个只够用的 TOML 键读取器。cc-switch 的 codex / grokbuild 配置是
// TOML 文本片段，只需要按「段 + 键」取字符串值；为一个读取动作引入完整 TOML 依赖不划算，
// 而契约只允许新增 modernc.org/sqlite 一个依赖。
package toml

import (
	"regexp"
	"strings"
)

// entry 是一条标量赋值。段为空表示文件顶层；段保留点号原文，于是
// [model."grok-4.6"] 写作 model."grok-4.6"，与文本里的写法一致。
type entry struct {
	section string
	name    string
	value   string
}

// keyName 归一化写入的键名：配置文件里 model 与 "model" 两种写法都要能命中。
func (e entry) keyName() string { return unquote(strings.TrimSpace(e.name)) }

var (
	sectionRe = regexp.MustCompile(`^\[([^\]]+)\]$`)
	assignRe  = regexp.MustCompile(`^([A-Za-z0-9_.\-"']+)\s*=\s*(.+)$`)
)

// Get 返回指定段下某个键的字符串值。找不到返回空串。
func Get(text, section, name string) string {
	for _, item := range read(text) {
		if item.section == section && item.name == name {
			return item.value
		}
	}
	return ""
}

// Top 返回顶层某个键的字符串值。
func Top(text, name string) string { return Get(text, "", name) }

// FirstInSections 按文件顺序返回第一个「键名匹配 + 段名匹配前缀」的值。
// 匹配键与段，且按文件顺序取第一个——grokbuild 的段里同时有 model 与 base_url，
// 只看段名会把 model 的值当成端点。
func FirstInSections(text, keyName string, sectionPrefixes ...string) (string, string) {
	for _, item := range read(text) {
		if item.keyName() != keyName || item.value == "" {
			continue
		}
		for _, prefix := range sectionPrefixes {
			if item.section == prefix || strings.HasPrefix(item.section, prefix) {
				return item.value, item.section
			}
		}
	}
	return "", ""
}

// FirstInSectionsAny 同 FirstInSections，但键名按候选顺序回退
// （grokbuild 的密钥字段名由 meta.apiKeyField 给出，不同供应商可能不同）。
func FirstInSectionsAny(text string, keyNames []string, sectionPrefixes ...string) (string, string) {
	for _, name := range keyNames {
		if value, section := FirstInSections(text, name, sectionPrefixes...); value != "" {
			return value, section
		}
	}
	return "", ""
}

// read 保序收集标量赋值。顺序必须保留：段前缀匹配要看文件里谁先出现，
// 用 map 迭代会随机取到别的段的值。行尾注释与数组表头（[[x]]）都不参与取值。
func read(text string) []entry {
	var out []entry
	section := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[[") {
			continue
		}
		if match := sectionRe.FindStringSubmatch(line); match != nil {
			section = unquote(strings.TrimSpace(match[1]))
			continue
		}
		match := assignRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		name := unquote(strings.TrimSpace(match[1]))
		value := unquote(strings.TrimSpace(stripComment(match[2])))
		if name == "" || value == "" {
			continue
		}
		out = append(out, entry{section: section, name: name, value: value})
	}
	return out
}

// stripComment 只裁掉不在引号内的 #，避免把 URL 里的片段当成注释起点。
func stripComment(value string) string {
	quoted := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '"':
			quoted = !quoted
		case '#':
			if !quoted {
				return value[:i]
			}
		}
	}
	return value
}

func unquote(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	return value
}
