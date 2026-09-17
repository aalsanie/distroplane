package domain

import "fmt"

type Plan struct {
	id              PlanID
	schemaVersion   string
	protocolVersion string
	release         Release
	targets         []Target
	operations      []Operation
}

func NewPlan(id PlanID, schemaVersion, protocolVersion string, release Release, targets []Target, operations []Operation) (Plan, error) {
	if !id.Valid() {
		return Plan{}, fmt.Errorf("plan ID is invalid")
	}
	if err := validateText("plan schema version", schemaVersion, false); err != nil {
		return Plan{}, err
	}
	if err := validateText("protocol version", protocolVersion, false); err != nil {
		return Plan{}, err
	}
	if !release.id.Valid() || len(release.artifacts) == 0 {
		return Plan{}, fmt.Errorf("release is invalid")
	}
	if len(targets) == 0 {
		return Plan{}, fmt.Errorf("plan must contain at least one target")
	}

	targetIndex := make(map[TargetID]Target, len(targets))
	for _, target := range targets {
		if !target.id.Valid() || !target.provider.Valid() || !target.configuration.Valid() {
			return Plan{}, fmt.Errorf("plan contains invalid target")
		}
		if _, exists := targetIndex[target.id]; exists {
			return Plan{}, fmt.Errorf("duplicate target ID %q", target.id)
		}
		targetIndex[target.id] = target
	}

	operationIndex := make(map[OperationID]Operation, len(operations))
	for _, operation := range operations {
		if !operation.id.Valid() {
			return Plan{}, fmt.Errorf("plan contains invalid operation")
		}
		if _, exists := operationIndex[operation.id]; exists {
			return Plan{}, fmt.Errorf("duplicate operation ID %q", operation.id)
		}
		target, exists := targetIndex[operation.targetID]
		if !exists {
			return Plan{}, fmt.Errorf("operation %q references unknown target %q", operation.id, operation.targetID)
		}
		if operation.provider != target.provider {
			return Plan{}, fmt.Errorf("operation %q provider does not match target", operation.id)
		}
		operationIndex[operation.id] = operation
	}

	for _, operation := range operations {
		for _, dependency := range operation.dependencies {
			if _, exists := operationIndex[dependency]; !exists {
				return Plan{}, fmt.Errorf("operation %q references unknown dependency %q", operation.id, dependency)
			}
		}
	}
	if err := validateAcyclic(operationIndex); err != nil {
		return Plan{}, err
	}

	return Plan{id: id, schemaVersion: schemaVersion, protocolVersion: protocolVersion, release: release, targets: append([]Target(nil), targets...), operations: append([]Operation(nil), operations...)}, nil
}

func (p Plan) ID() PlanID              { return p.id }
func (p Plan) SchemaVersion() string   { return p.schemaVersion }
func (p Plan) ProtocolVersion() string { return p.protocolVersion }
func (p Plan) Release() Release        { return p.release }
func (p Plan) Targets() []Target       { return append([]Target(nil), p.targets...) }
func (p Plan) Operations() []Operation { return append([]Operation(nil), p.operations...) }

func validateAcyclic(operations map[OperationID]Operation) error {
	indegree := make(map[OperationID]int, len(operations))
	dependents := make(map[OperationID][]OperationID, len(operations))
	queue := make([]OperationID, 0, len(operations))
	for id, operation := range operations {
		indegree[id] = len(operation.dependencies)
		if len(operation.dependencies) == 0 {
			queue = append(queue, id)
		}
		for _, dependency := range operation.dependencies {
			dependents[dependency] = append(dependents[dependency], id)
		}
	}
	processed := 0
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		processed++
		for _, dependent := range dependents[id] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}
	if processed != len(operations) {
		return fmt.Errorf("operation dependency graph contains a cycle")
	}
	return nil
}
