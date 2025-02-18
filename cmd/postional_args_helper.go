/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */
package cmd

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
)

type PositionalArgSpec struct {
	Name        string  // Name of the argument (eg, ENVIRONMENT)
	Description string  // Description of the argument
	IsRequired  bool    // Is the argument required (or optional)?
	ValuePtr    *string // Pointer to the parsed value. \todo support more types?
}

type PositionalArgs struct {
	Specs                []PositionalArgSpec // Array of arguments for the command
	ExtraArgsPtr         *[]string           // Pointer to extra args (if specified)
	ExtraArgsDescription string              // Description of extra args (if any)
}

func (args *PositionalArgs) AddStringArgument(valuePtr *string, name string, description string) {
	// \todo check that no optionals are specified
	// Append to arg specs array.
	argSpec := PositionalArgSpec{
		Name:        name,
		Description: description,
		IsRequired:  true,
		ValuePtr:    valuePtr,
	}
	args.Specs = append(args.Specs, argSpec)
}

func (args *PositionalArgs) AddStringArgumentOpt(valuePtr *string, name string, description string) {
	// Append to arg specs array.
	argSpec := PositionalArgSpec{
		Name:        name,
		Description: description,
		IsRequired:  false,
		ValuePtr:    valuePtr,
	}
	args.Specs = append(args.Specs, argSpec)
}

func (args *PositionalArgs) SetExtraArgs(extraArgsPtr *[]string, description string) {
	if args.ExtraArgsPtr != nil {
		log.Panic().Msgf("Duplicate extra args specified: '%s'", description)
	}

	args.ExtraArgsPtr = extraArgsPtr
	args.ExtraArgsDescription = description
}

func (args *PositionalArgs) GetHelpText() string {
	if len(args.Specs) == 0 && args.ExtraArgsPtr == nil {
		return "No positional arguments are required for this command."
	}

	lines := []string{"Expected arguments:"}

	// Iterate through the Specs to generate help text for each argument
	for _, spec := range args.Specs {
		optionalText := ""
		if !spec.IsRequired {
			optionalText = " (optional)"
		}
		lines = append(lines, fmt.Sprintf("  - %s%s -- %s", spec.Name, optionalText, spec.Description))
	}

	// Handle extra arguments if they are allowed
	if args.ExtraArgsPtr != nil {
		lines = append(lines, fmt.Sprintf("  - EXTRA_ARGS (optional) -- %s", args.ExtraArgsDescription))
	}

	return strings.Join(lines, "\n")
}

func (args *PositionalArgs) ParseCommandLine(argv []string) error {
	// Parse all positional arguments.
	// log.Info().Msgf("Parsing command line: %v", argv)
	srcNdx := 0
	for _, argSpec := range args.Specs {
		// Check if the argument is provided.
		hasArg := len(argv) > srcNdx
		if hasArg {
			// Store the argument.
			*argSpec.ValuePtr = argv[srcNdx]
			srcNdx += 1
		} else {
			// If argument is reuqired, error out.
			if argSpec.IsRequired {
				// \todo bad error message
				return fmt.Errorf("Provided %d arguments, expecting %d", len(argv), len(args.Specs))
			}
		}
	}

	// Parse remaining arguments as extra args.
	hasExtraArgs := len(argv) > srcNdx
	if hasExtraArgs {
		// Extra arguments on command line, parse them into the extra args output if command
		// allows extra args. Otherwise, error out on unexpected extra args.
		if args.ExtraArgsPtr != nil {
			*args.ExtraArgsPtr = argv[srcNdx:]
		} else {
			return fmt.Errorf("UNEXPECTED EXTRA ARGS PROVIDED: %v", argv[srcNdx:])
		}
	} else {
		// No extra arguments on the command line. Store an empty array anyway (if extra args
		// allowed by command).
		if args.ExtraArgsPtr != nil {
			*args.ExtraArgsPtr = []string{}
		}
	}

	return nil
}

// BASE OPTS

type UsePositionalArgs struct {
	args PositionalArgs
}

func (o *UsePositionalArgs) Arguments() *PositionalArgs {
	return &o.args
}
