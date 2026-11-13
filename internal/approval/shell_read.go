package approval

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (p Policy) evaluateRead(args []string, cwd string) Result {
	name := filepath.Base(args[0])
	if name != "ls" && name != "cat" && name != "head" && name != "grep" {
		return review("shell.command_unsupported")
	}
	operands, ok := readOperands(name, args[1:])
	if !ok {
		return review("shell.arguments_unsupported")
	}
	if name == "ls" && len(operands) == 0 {
		operands = []string{"."}
	}
	for _, operand := range operands {
		if !p.readPathSupported(name, cwd, operand) {
			return review("shell.path_unvalidated")
		}
	}
	return approve("shell.supported")
}

func (p Policy) readPathSupported(name, cwd, operand string) bool {
	path, ok := p.resolveProjectPath(cwd, operand, false)
	if !ok {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular() || name == "ls" && info.IsDir()
}

func readOperands(name string, args []string) ([]string, bool) {
	var operands []string
	pattern := name != "grep"
	options := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(arg, "-") {
			consumed, ok := readOption(name, args[i:])
			if !ok {
				return nil, false
			}
			if name == "grep" && arg == "-e" {
				pattern = true
			}
			i += consumed - 1
			continue
		}
		if !pattern {
			pattern = true
			continue
		}
		operands = append(operands, arg)
	}
	return operands, pattern
}

func readOption(name string, args []string) (int, bool) {
	arg := args[0]
	switch name {
	case "cat":
		return 1, supportedCatOption(arg)
	case "ls":
		return 1, len(arg) > 1 && strings.Trim(arg[1:], "lahtd1") == ""
	case "head":
		return headOption(args)
	case "grep":
		return grepOption(args)
	default:
		return 0, false
	}
}

func grepOption(args []string) (int, bool) {
	if args[0] == "-e" {
		return 2, len(args) > 1
	}
	switch args[0] {
	case "-n", "-i", "-q", "-F", "-E", "-v", "-c":
		return 1, true
	default:
		return 0, false
	}
}

func supportedCatOption(arg string) bool {
	switch arg {
	case "-n", "-b", "-s":
		return true
	default:
		return false
	}
}

func headOption(args []string) (int, bool) {
	if args[0] != "-n" || len(args) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(args[1])
	return 2, err == nil && n > 0 && n <= 100000
}
