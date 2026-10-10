// Run from the repository root to export the exact production vision contract.
package main

import (
	"encoding/json"
	"github.com/vaporon4a/movie-helper/internal/ai"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "evaluate" {
		var input struct {
			Result ai.Selection `json:"result"`
			Count  int          `json:"count"`
		}
		if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
			panic(err)
		}
		if err := ai.ValidateMemeSelection(input.Result, input.Count); err != nil {
			panic(err)
		}
		decisions := map[int]string{}
		for _, r := range input.Result.Reviews {
			decisions[*r.Index] = ai.MemeDecision(r)
		}
		if err := json.NewEncoder(os.Stdout).Encode(decisions); err != nil {
			panic(err)
		}
		return
	}

	parts := []ai.Part{{Inline: &ai.Inline{}}, {Inline: &ai.Inline{}}, {Inline: &ai.Inline{}}}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"instruction": ai.MemeReviewInstruction, "schema": ai.SelectionSchema(parts), "policy": ai.MemeReviewVersion}); err != nil {
		panic(err)
	}
}
