// Package errors provides enhanced error handling for minibp with categorized errors,
// helpful suggestions, and context information.
package errors

import (
	"fmt"
	"strings"
)

// ErrorCategory represents the type of error.
// Categories are used to classify errors and provide specific suggestions
// for different error types in Blueprint files.
type ErrorCategory int

const (
	Uncategorized       ErrorCategory = iota // Unclassified or unknown error type
	SyntaxError                              // Syntax error in Blueprint files
	DependencyError                          // Dependency resolution error
	ConfigurationError                       // Configuration value error
	FileNotFoundError                        // File not found error
	CircularDependency                       // Circular dependency between modules
	DuplicateDefinition                      // Duplicate module definition
	TypeMismatch                             // Property type mismatch
	MissingProperty                          // Required property not specified
	InvalidValue                             // Invalid property value
)

// String returns the string representation of ErrorCategory.
// Returns the category name as a string, e.g., "SyntaxError", "DependencyError".
//
// Edge case: Uncategorized is returned for unknown iota values
// (should not happen with proper const definitions but safe fallback).
func (c ErrorCategory) String() string {
	switch c {
	case SyntaxError:
		return "SyntaxError"
	case DependencyError:
		return "DependencyError"
	case ConfigurationError:
		return "ConfigurationError"
	case FileNotFoundError:
		return "FileNotFoundError"
	case CircularDependency:
		return "CircularDependency"
	case DuplicateDefinition:
		return "DuplicateDefinition"
	case TypeMismatch:
		return "TypeMismatch"
	case MissingProperty:
		return "MissingProperty"
	case InvalidValue:
		return "InvalidValue"
	default:
		return "Uncategorized"
	}
}

// ErrorSeverity represents the severity level of an error.
// This helps categorize errors during build processing to determine
// whether the build should fail or continue with warnings.
type ErrorSeverity int

const (
	Error   ErrorSeverity = iota // Error level - build should fail
	Warning                      // Warning level - build may succeed with caution
	Info                         // Info level - informational message only
)

// String returns the string representation of ErrorSeverity.
// Returns the severity name as a string, e.g., "Error", "Warning", "Info".
//
// Edge case: Unknown is returned for unknown iota values.
func (s ErrorSeverity) String() string {
	switch s {
	case Error:
		return "Error"
	case Warning:
		return "Warning"
	case Info:
		return "Info"
	default:
		return "Unknown"
	}
}

// Location represents a position in source code.
// Used to pinpoint the exact location of an error in a .bp file.
// Line and Column are 1-indexed to match text editor conventions.
type Location struct {
	File    string // File name relative to the build root (empty if unknown)
	Line    int    // Line number (1-indexed, 0 if unknown)
	Column  int    // Column number (1-indexed, 0 if unknown)
	Content string // Content of the current line (may be empty if not loaded)
}

// ErrorContext provides additional context for an error.
// Contains supplementary information to help developers understand and fix errors.
type ErrorContext struct {
	Snippet         string   // Code snippet (surrounding lines from source file)
	RelatedFiles    []string // Related files that may be involved in the error
	DependencyChain []string // Dependency chain leading to the error (for circular dependencies)
}

// BuildError represents a structured build error with rich context.
// This is the main error type used throughout minibp to provide detailed error information,
// including source location, code snippets, and actionable suggestions.
//
// A BuildError can be created using NewError() or convenience constructors like
// Syntax(), Dependency(), Circular(), etc. All support method chaining:
//
//	errors.Syntax("unexpected token").
//	    WithLocation("Android.bp", 42, 1).
//	    WithContent(`    name: "module"`).
//	    WithSuggestion("Property name should be a string")
type BuildError struct {
	Category   ErrorCategory // Category of the error (e.g., SyntaxError, DependencyError)
	Severity   ErrorSeverity // Severity level (Error, Warning, Info)
	Message    string        // Main error message describing the issue
	Location   *Location     // Source location where the error occurred (nil if unknown)
	Context    *ErrorContext // Additional context information (nil if not provided)
	Suggestion string        // Suggestion for how to fix the error (optional)
	Cause      error         // Underlying cause of the error (wrapped error, nil if not wrapped)
}

// NewError creates a new BuildError with the given category and message.
// This is the constructor for creating BuildError instances.
// The error is initialized with Error severity level.
//
// Parameters:
//   - category: The ErrorCategory classifying this error
//   - message: The error message describing the issue
//
// Returns a pointer to the newly created BuildError with default Error severity.
// Returns a pointer (not value) to allow method chaining with With* builders.
//
// Note:
//   - Severity defaults to Error; use WithSeverity to change if needed
//   - All other fields (Location, Context, Suggestion, Cause) default to nil
//   - The returned error implements the standard error interface
func NewError(category ErrorCategory, message string) *BuildError {
	return &BuildError{
		Category: category,
		Severity: Error,
		Message:  message,
	}
}

// WithLocation sets the location for the error.
// Chainable method that returns the same BuildError for fluent API.
//
// Parameters:
//   - file: Path to the source file
//   - line: Line number in the source file (1-indexed)
//   - column: Column number in the source file (1-indexed)
//
// Returns the same BuildError for method chaining.
func (e *BuildError) WithLocation(file string, line, column int) *BuildError {
	e.Location = &Location{
		File:   file,
		Line:   line,
		Column: column,
	}
	return e
}

// WithContent sets the content at the error location.
// This stores the actual source line content for display in formatted error output.
// If Location is nil, creates a new empty Location to store the content.
//
// Parameters:
//   - content: The actual source code line content
//
// Returns the same BuildError for method chaining.
func (e *BuildError) WithContent(content string) *BuildError {
	if e.Location == nil {
		e.Location = &Location{}
	}
	e.Location.Content = content
	return e
}

// WithContext sets additional context for the error.
// Provides extra information such as code snippets, related files, or dependency chains.
//
// Parameters:
//   - ctx: ErrorContext containing supplementary information
//
// Returns the same BuildError for method chaining.
func (e *BuildError) WithContext(ctx *ErrorContext) *BuildError {
	e.Context = ctx
	return e
}

// WithSuggestion sets a suggestion for fixing the error.
// Provides actionable advice to help developers resolve the issue.
//
// Parameters:
//   - suggestion: Text suggestion for fixing the error
//
// Returns the same BuildError for method chaining.
func (e *BuildError) WithSuggestion(suggestion string) *BuildError {
	e.Suggestion = suggestion
	return e
}

// WithCause sets the underlying cause of the error.
// Wraps a lower-level error that contributed to this BuildError.
//
// Parameters:
//   - cause: The underlying error that caused this BuildError
//
// Returns the same BuildError for method chaining.
func (e *BuildError) WithCause(cause error) *BuildError {
	e.Cause = cause
	return e
}

// Format formats the error for display.
// Generates a human-readable error message with all available context:
//   - Error category and severity
//   - Source location with file:line:column
//   - Code snippet with caret pointing to error position
//   - Dependency chain (for circular dependency errors)
//   - Suggestion for fixing
//   - Underlying cause
//
// Output format:
//
//	[SyntaxError] Error: unexpected token
//	 --> Android.bp:42:5
//	 |
//	42 |     name: "lib"
//	 |     ^^^^^
//	 |
//	Suggestion: Property name should be enclosed in double quotes
//
// Returns a formatted string representation of the error suitable for display.
// Returns empty string if Message is empty (shouldn't happen in normal use).
//
// Edge cases:
//   - Missing Location: location section is completely omitted
//   - Location with empty Content: only file:line shown, no code snippet
//   - Missing Context: snippet and dependency chain omitted
//   - Missing Suggestion/Cause: respective sections omitted
//   - Empty Message: returns only "[category] Severity: " (unusual)
func (e *BuildError) Format() string {
	var sb strings.Builder

	// Error type and message
	// Format: [SyntaxError] Error: unexpected token
	sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", e.Category, e.Severity, e.Message))

	// Location information
	// Only include if File is set; Line=0 or Column=0 allowed but may omit column
	if e.Location != nil && e.Location.File != "" {
		// Build location string: "file" or "file:line" or "file:line:column"
		loc := fmt.Sprintf("%s:%d", e.Location.File, e.Location.Line)
		if e.Location.Column > 0 {
			loc += fmt.Sprintf(":%d", e.Location.Column)
		}
		sb.WriteString(fmt.Sprintf(" --> %s\n", loc))

		// Code content and pointer
		// Only show if Content is provided; shows the line with error marker
		if e.Location.Content != "" {
			sb.WriteString(" |\n")
			sb.WriteString(fmt.Sprintf("%d | %s\n", e.Location.Line, e.Location.Content))
			sb.WriteString(" | ")
			// Point to column position with ^^^ markers
			sb.WriteString(strings.Repeat(" ", e.Location.Column-1))
			sb.WriteString(strings.Repeat("^", 5))
			sb.WriteString("\n")
		}
	}

	// Context code snippet
	// Additional lines surrounding the error, if provided
	if e.Context != nil && e.Context.Snippet != "" {
		sb.WriteString(" |\n")
		sb.WriteString(e.Context.Snippet)
		sb.WriteString(" |\n")
	}

	// Dependency chain (for circular dependencies)
	// Shows the cycle: "a -> b -> c -> a"
	if e.Context != nil && len(e.Context.DependencyChain) > 0 {
		sb.WriteString("Dependency chain:\n")
		for i, dep := range e.Context.DependencyChain {
			indent := strings.Repeat(" ", i)
			sb.WriteString(fmt.Sprintf("%s -> %s\n", indent, dep))
		}
	}

	// Suggestion
	// Actionable advice for fixing the error
	if e.Suggestion != "" {
		sb.WriteString(fmt.Sprintf("\nSuggestion: %s\n", e.Suggestion))
	}

	// Underlying cause
	// Wrapped error from deeper in the stack
	if e.Cause != nil {
		sb.WriteString(fmt.Sprintf("\nCause: %v\n", e.Cause))
	}

	return sb.String()
}

// Error implements the error interface.
// Returns the formatted error message as a string.
// This method allows BuildError to be used as a standard Go error.
//
// Returns the formatted error string.
func (e *BuildError) Error() string {
	return e.Format()
}

// Helper functions for creating specific error types

// Syntax creates a syntax error with the given message.
// Use this for parsing errors, malformed expressions, or invalid syntax in .bp files.
//
// Parameters:
//   - msg: The syntax error message describing the issue
//
// Returns a BuildError with SyntaxError category.
func Syntax(msg string) *BuildError {
	return NewError(SyntaxError, msg)
}

// Dependency creates a dependency error with the given message.
// Use this for missing dependencies, invalid module references, or dependency resolution failures.
//
// Parameters:
//   - msg: The dependency error message describing the issue
//
// Returns a BuildError with DependencyError category.
func Dependency(msg string) *BuildError {
	return NewError(DependencyError, msg)
}

// Circular creates a circular dependency error.
// Detects and reports circular dependencies between modules.
//
// Parameters:
//   - chain: Ordered list of module names showing the dependency cycle
//
// Returns a BuildError with CircularDependency category, including the dependency chain
// and a suggestion to break the cycle.
func Circular(chain []string) *BuildError {
	return NewError(CircularDependency, "Circular dependency detected").
		WithContext(&ErrorContext{DependencyChain: chain}).
		WithSuggestion("Check the dependency chain and remove one dependency to break the cycle")
}

// NotFound creates a file not found error.
// Use this when a required file does not exist at the specified path.
//
// Parameters:
//   - file: The file path that was not found
//
// Returns a BuildError with FileNotFoundError category and a suggestion to verify the path.
func NotFound(file string) *BuildError {
	return NewError(FileNotFoundError, fmt.Sprintf("File not found: %s", file)).
		WithSuggestion("Check if the file path is correct or if the file exists")
}

// Duplicate creates a duplicate definition error.
// Use this when a module or property is defined multiple times.
//
// Parameters:
//   - name: The name of the duplicate definition
//   - file: The file where the duplicate was found
//   - line: The line number where the duplicate was found
//
// Returns a BuildError with DuplicateDefinition category and suggestion pointing to the original.
func Duplicate(name, file string, line int) *BuildError {
	return NewError(DuplicateDefinition, fmt.Sprintf("Duplicate definition: %s", name)).
		WithSuggestion(fmt.Sprintf("Look for previous definition near %s:%d", file, line))
}

// Missing creates a missing property error.
// Use this when a required property is not specified in a module.
//
// Parameters:
//   - moduleName: The name of the module missing the property
//   - propertyName: The name of the required property that is missing
//
// Returns a BuildError with MissingProperty category.
func Missing(moduleName, propertyName string) *BuildError {
	return NewError(MissingProperty, fmt.Sprintf("Module %s is missing required property: %s", moduleName, propertyName))
}

// Invalid creates an invalid value error.
// Use this when a property has an invalid value that cannot be parsed or is out of allowed range.
//
// Parameters:
//   - moduleName: The name of the module with the invalid value
//   - propertyName: The name of the property with the invalid value
//   - value: The invalid value that was provided
//   - reason: The reason why the value is invalid
//
// Returns a BuildError with InvalidValue category.
func Invalid(moduleName, propertyName, value, reason string) *BuildError {
	return NewError(InvalidValue, fmt.Sprintf("Module %s has invalid value for property %s: %s (%s)", moduleName, propertyName, value, reason))
}

// Config creates a configuration error.
// Use this for invalid configuration options or Settings errors.
//
// Parameters:
//   - msg: The configuration error message
//
// Returns a BuildError with ConfigurationError category.
func Config(msg string) *BuildError {
	return NewError(ConfigurationError, msg)
}

// Type creates a type mismatch error.
// Use this when a property value does not match the expected type.
//
// Parameters:
//   - moduleName: The name of the module with the type mismatch
//   - propertyName: The name of the property with the type mismatch
//   - expected: The expected type (e.g., "string", "list of strings")
//   - actual: The actual type that was provided
//
// Returns a BuildError with TypeMismatch category.
func Type(moduleName, propertyName, expected, actual string) *BuildError {
	return NewError(TypeMismatch, fmt.Sprintf("Module %s property %s type mismatch: expected %s, got %s", moduleName, propertyName, expected, actual))
}
