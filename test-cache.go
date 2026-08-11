package main

import (
	"fmt"
	"os"
)

func main() {
	dir, _ := os.UserCacheDir()
	fmt.Println(dir)
}
