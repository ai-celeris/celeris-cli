package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// samplingFlags holds the request parameters shared by both completion
// endpoints. Pointer-valued parameters are sent only when their flag was set,
// leaving service defaults untouched.
type samplingFlags struct {
	maxTokens        int
	temperature      float64
	topP             float64
	seed             int
	stop             []string
	presencePenalty  float64
	frequencyPenalty float64
}

func (s *samplingFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.IntVar(&s.maxTokens, "max-tokens", defaultMaxTokens,
		fmt.Sprintf("completion budget, 0 to omit and take the service default (max %d, shared with the prompt)", maxTokensLimit))
	f.Float64Var(&s.temperature, "temperature", 0, "sampling temperature")
	f.Float64Var(&s.topP, "top-p", 0, "nucleus sampling probability mass")
	f.IntVar(&s.seed, "seed", 0, "best-effort deterministic sampling seed")
	f.StringArrayVar(&s.stop, "stop", nil, "stop sequence (repeatable)")
	f.Float64Var(&s.presencePenalty, "presence-penalty", 0, "presence penalty")
	f.Float64Var(&s.frequencyPenalty, "frequency-penalty", 0, "frequency penalty")
}

func (s *samplingFlags) validate() error {
	return validateMaxTokens(s.maxTokens)
}

func floatIfSet(cmd *cobra.Command, name string, v float64) *float64 {
	if cmd.Flags().Changed(name) {
		return &v
	}
	return nil
}

func intIfSet(cmd *cobra.Command, name string, v int) *int {
	if cmd.Flags().Changed(name) {
		return &v
	}
	return nil
}
