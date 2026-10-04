package util

import "fmt"

func PrintVars(vs ...any) {
	for _, v := range vs {
		fmt.Printf("[DEBUG] - %+v \n", v)
	}
}
