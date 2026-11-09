package winsvc

import "fmt"

func setupDefinition(args []string) (Definition, error) {
	definition := Definition{}
	if len(args) < 2 || args[1] != "run" || (len(args)-2)%2 != 0 {
		return definition, fmt.Errorf("现有 lgmon 服务参数无法识别，拒绝替换或移除")
	}
	definition.Executable = args[0]
	seen := map[string]bool{}
	for i := 2; i < len(args); i += 2 {
		if seen[args[i]] {
			return Definition{}, fmt.Errorf("现有 lgmon 服务参数重复，拒绝替换或移除")
		}
		seen[args[i]] = true
		switch args[i] {
		case "--home":
			definition.Home = args[i+1]
		case "--role":
			definition.Role = args[i+1]
		case "--log-level":
			definition.LogLevel = args[i+1]
		case "--listen":
			definition.Listen = args[i+1]
		case "--monitor":
			definition.Monitor = args[i+1]
		default:
			return Definition{}, fmt.Errorf("现有 lgmon 服务含未知参数，拒绝替换或移除")
		}
	}
	if definition.Home == "" || definition.Role == "" {
		return Definition{}, fmt.Errorf("现有 lgmon 服务未提供明确 home 和角色，拒绝替换或移除")
	}
	return definition, nil
}
