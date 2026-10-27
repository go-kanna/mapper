// Package gen writes the functions declared as the methods of interfaces marked
// //kanna:mapper. cmd/kanna-mapper runs it.
//
// Each function maps its source to its destination field by field, calling
// the functions listed in //kanna:convert lists for the types Go cannot
// convert on its own. Nothing is looked up at run time: the generated code
// calls the user's types and functions directly, and a mapping that cannot be
// written stops generation instead.
package gen
