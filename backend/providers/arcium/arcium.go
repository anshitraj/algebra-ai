package arcium

import (
	"context"

	"github.com/project-algebra/algebra/internal/domain/confidential"
	"github.com/project-algebra/algebra/internal/domain/shared"
)

// ArciumProvider is the adapter slot for a real Arcium deployment
// (https://docs.arcium.com). No Arcium program ID, cluster configuration,
// or client credentials exist in this environment — every method fails
// closed with shared.ErrNotImplemented. Per the mandate (§26/§69): do not
// invent a deployed program, and do not couple Algebra's core to Arcium —
// LocalEncryptedProvider (local.go) is the fully-functional default, and
// this type only becomes relevant once a real Arcium program/environment is
// configured and a specific confidential-compute workload justifies it.
type ArciumProvider struct {
	ClusterEndpoint string
	ProgramID       string
}

func NewArciumProvider(clusterEndpoint, programID string) *ArciumProvider {
	return &ArciumProvider{ClusterEndpoint: clusterEndpoint, ProgramID: programID}
}

func (p *ArciumProvider) Mode() confidential.Mode { return confidential.ModeArcium }

func (p *ArciumProvider) EncryptAttribute(context.Context, []byte, []byte) ([]byte, error) {
	return nil, shared.ErrNotImplemented
}

func (p *ArciumProvider) DecryptAttribute(context.Context, []byte, []byte) ([]byte, error) {
	return nil, shared.ErrNotImplemented
}

func (p *ArciumProvider) EvaluateThresholdConfidentially(context.Context, []byte, []byte, int64) (bool, error) {
	return false, shared.ErrNotImplemented
}

var _ confidential.Provider = (*ArciumProvider)(nil)
