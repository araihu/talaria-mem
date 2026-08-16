package application

import (
	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

type Actor string

const (
	ActorCLI    Actor = "cli"
	ActorMCP    Actor = "mcp"
	ActorImport Actor = "import"
	ActorSystem Actor = "system"
)

func (actor Actor) Valid() bool {
	return actor == ActorCLI || actor == ActorMCP || actor == ActorImport || actor == ActorSystem
}

type Operation string

const (
	OperationCreate  Operation = "create"
	OperationUpdate  Operation = "update"
	OperationConfirm Operation = "confirm"
	OperationPin     Operation = "pin"
	OperationForget  Operation = "forget"
	OperationRestore Operation = "restore"
	OperationReview  Operation = "review"
)

func trustFor(actor Actor, requested bool) (domain.Trust, error) {
	if !actor.Valid() {
		return "", domain.NewError(domain.CodeValidation, "invalid actor", false)
	}
	if requested && actor != ActorCLI {
		return "", domain.NewError(domain.CodeValidation, "only CLI may assert verified trust", false)
	}
	if requested {
		return domain.TrustVerified, nil
	}
	return domain.TrustUnverified, nil
}

func validateTrustKind(actor Actor, kind domain.MemoryKind, requested bool) (domain.Trust, error) {
	if kind == domain.MemoryKindStandingInstruction && (actor != ActorCLI || !requested) {
		return "", domain.NewError(domain.CodeValidation, "standing instruction requires CLI verified assertion", false)
	}
	return trustFor(actor, requested)
}
