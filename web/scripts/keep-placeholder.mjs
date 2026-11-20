// Vite empties dist/ on build; restore the placeholder the Go embed needs in a
// fresh clone so `go build` works before the UI has ever been built.
import { writeFileSync } from "node:fs";
writeFileSync(new URL("../dist/.gitkeep", import.meta.url), "");
