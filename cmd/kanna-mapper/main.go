// Command kanna-mapper generates the functions declared as the methods of
// interfaces marked //kanna:mapper.
package main

import (
	"os"

	"github.com/go-kanna/mapper/internal/gen"
)

func main() {
	os.Exit(gen.Command().Main(os.Args[1:]))
}
