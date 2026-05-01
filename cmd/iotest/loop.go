package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

var templateRe = regexp.MustCompile(`\{\{(.+?)\}\}`)

// resolveTemplate replaces {{expr}} in s with evaluated values.
func resolveTemplate(s string, vars map[string]interface{}) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return templateRe.ReplaceAllStringFunc(s, func(match string) string {
		expr := strings.TrimSpace(match[2 : len(match)-2])

		// {{item}} — lookup from items array
		if expr == "item" {
			if v, ok := vars["item"]; ok {
				return fmt.Sprintf("%v", v)
			}
			return match
		}

		// {{sysfs:/path == "value"}} — runtime sysfs comparison (handled by condition evaluator)
		if strings.HasPrefix(expr, "sysfs:") {
			return match // not resolved here, only in conditions
		}

		// {{last_error}} — runtime variable
		if expr == "last_error" {
			if v, ok := vars["last_error"]; ok {
				return fmt.Sprintf("%v", v)
			}
			return ""
		}

		// Arithmetic expression involving i: {{i}}, {{i+1}}, {{i*4096}}, {{i % 2}}
		result, err := evalArithmetic(expr, vars)
		if err == nil {
			return strconv.FormatInt(result, 10)
		}

		// Direct variable lookup
		if v, ok := vars[expr]; ok {
			return fmt.Sprintf("%v", v)
		}

		return match
	})
}

// evalArithmetic evaluates simple integer arithmetic expressions.
// Supports: i, +, -, *, /, %, parentheses, and comparisons (==, !=, >, <, >=, <=).
func evalArithmetic(expr string, vars map[string]interface{}) (int64, error) {
	// Replace variable names with their values
	resolved := expr
	for k, v := range vars {
		switch val := v.(type) {
		case int:
			resolved = replaceVar(resolved, k, strconv.Itoa(val))
		case int64:
			resolved = replaceVar(resolved, k, strconv.FormatInt(val, 10))
		case float64:
			resolved = replaceVar(resolved, k, strconv.FormatInt(int64(val), 10))
		}
	}

	return evalExpr(resolved)
}

// replaceVar replaces variable name with value, being careful about word boundaries.
func replaceVar(expr, name, value string) string {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	return re.ReplaceAllString(expr, value)
}

// evalExpr evaluates a simple arithmetic expression string.
func evalExpr(expr string) (int64, error) {
	expr = strings.TrimSpace(expr)

	// Handle comparison operators (return 1 for true, 0 for false)
	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		if idx := strings.Index(expr, op); idx >= 0 {
			left, err := evalExpr(expr[:idx])
			if err != nil {
				return 0, err
			}
			right, err := evalExpr(expr[idx+len(op):])
			if err != nil {
				return 0, err
			}
			var result bool
			switch op {
			case "==":
				result = left == right
			case "!=":
				result = left != right
			case ">=":
				result = left >= right
			case "<=":
				result = left <= right
			case ">":
				result = left > right
			case "<":
				result = left < right
			}
			if result {
				return 1, nil
			}
			return 0, nil
		}
	}

	// Use Go's AST parser for arithmetic
	node, err := parser.ParseExpr(expr)
	if err != nil {
		return 0, fmt.Errorf("parse error: %s", expr)
	}
	return evalASTNode(node)
}

func evalASTNode(node ast.Expr) (int64, error) {
	switch n := node.(type) {
	case *ast.BasicLit:
		if n.Kind == token.INT {
			return strconv.ParseInt(n.Value, 10, 64)
		}
		return 0, fmt.Errorf("unsupported literal: %s", n.Value)
	case *ast.ParenExpr:
		return evalASTNode(n.X)
	case *ast.UnaryExpr:
		val, err := evalASTNode(n.X)
		if err != nil {
			return 0, err
		}
		if n.Op == token.SUB {
			return -val, nil
		}
		return val, nil
	case *ast.BinaryExpr:
		left, err := evalASTNode(n.X)
		if err != nil {
			return 0, err
		}
		right, err := evalASTNode(n.Y)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		case token.REM:
			if right == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			return left % right, nil
		}
	}
	return 0, fmt.Errorf("unsupported expression")
}

// evaluateCondition evaluates a condition string, returning true/false.
// Supports:
//   - {{i % 2 == 0}} → arithmetic comparison
//   - {{sysfs:/path == "value"}} → read sysfs and compare
//   - {{last_error != ""}} → check runtime variable
func evaluateCondition(condition string, vars map[string]interface{}) bool {
	cond := strings.TrimSpace(condition)

	// Remove {{ }} wrapper if present
	if strings.HasPrefix(cond, "{{") && strings.HasSuffix(cond, "}}") {
		cond = strings.TrimSpace(cond[2 : len(cond)-2])
	}

	// sysfs comparison: sysfs:/path == "value"
	if strings.HasPrefix(cond, "sysfs:") {
		return evaluateSysfsCondition(cond)
	}

	// String comparison: last_error != ""
	if strings.Contains(cond, `""`) || strings.Contains(cond, `"`) {
		return evaluateStringCondition(cond, vars)
	}

	// Arithmetic comparison
	result, err := evalArithmetic(cond, vars)
	if err != nil {
		return false
	}
	return result != 0
}

func evaluateSysfsCondition(cond string) bool {
	// sysfs:/sys/devices/.../status == "0x1"
	parts := strings.SplitN(cond, " ", 3)
	if len(parts) < 3 {
		return false
	}
	path := strings.TrimPrefix(parts[0], "sysfs:")
	op := parts[1]

	expected := strings.Trim(parts[2], `"`)

	data, err := readSysfs(path)
	if err != nil {
		return false
	}
	actual := strings.TrimSpace(data)

	switch op {
	case "==":
		return actual == expected
	case "!=":
		return actual != expected
	default:
		return false
	}
}

func evaluateStringCondition(cond string, vars map[string]interface{}) bool {
	// Simple pattern: varname != "" or varname == ""
	for _, op := range []string{"!=", "=="} {
		if idx := strings.Index(cond, op); idx >= 0 {
			varName := strings.TrimSpace(cond[:idx])
			expected := strings.Trim(strings.TrimSpace(cond[idx+len(op):]), `"`)

			actual := ""
			if v, ok := vars[varName]; ok {
				actual = fmt.Sprintf("%v", v)
			}

			switch op {
			case "==":
				return actual == expected
			case "!=":
				return actual != expected
			}
		}
	}
	return false
}
