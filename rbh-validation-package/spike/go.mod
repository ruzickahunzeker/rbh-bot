module example.invalid/rbh-validation-spike-source

go 1.26.0

toolchain go1.26.6

// The verification runner copies sdk_smoke_test.go into a disposable module and
// supplies pinned SDK replacements there. This module boundary keeps the source
// fixture out of the production rbh-bot module's ./... package pattern.

