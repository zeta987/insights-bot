package openaimock

import "context"

// SarcasticCondense provides a simple stub implementation to satisfy the openai.Client interface.
// This method can be overridden in tests by assigning a custom function to MockClient.SarcasticCondenseStub.
func (fake *MockClient) SarcasticCondense(ctx context.Context, chatHistory string) (string, error) { //nolint:unused
	fake.recordInvocation("SarcasticCondense", []interface{}{ctx, chatHistory})
	// Return zero values by default.
	return "", nil
}
