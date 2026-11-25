package scrcpy

import "fmt"

func scrcpyParamsToArgs(params map[string]string) []string {
	var args []string
	for k := range params {
		//如果首字母大写，则跳过
		if len(k) > 0 && k[0] >= 'A' && k[0] <= 'Z' {
			continue
		}

		if v := params[k]; v != "" {
			args = append(args, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return args
}

