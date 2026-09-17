package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

type Handler interface {
	Describe(context.Context, DescribeRequest) (DescribeResponse, *ProviderError)
	Plan(context.Context, PlanRequest) (PlanResponse, *ProviderError)
	Apply(context.Context, ApplyRequest) (ApplyResponse, *ProviderError)
	Reconcile(context.Context, ReconcileRequest) (ReconcileResponse, *ProviderError)
}

func ServeOnce(ctx context.Context, input io.Reader, output io.Writer, handler Handler, codec Codec) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	if handler == nil {
		return fmt.Errorf("handler must not be nil")
	}
	request, err := codec.DecodeRequest(input)
	if err != nil {
		return err
	}
	if err := request.CheckVersion(); err != nil {
		return writeError(output, codec, request, NewProviderError(ErrorProtocol, err.Error(), false))
	}
	if !request.Operation.Valid() {
		return writeError(output, codec, request, NewProviderError(ErrorProtocol, fmt.Sprintf("unsupported operation %q", request.Operation), false))
	}

	payload, providerErr := dispatch(ctx, handler, request)
	if providerErr != nil {
		if err := providerErr.Validate(); err != nil {
			providerErr = &ProviderError{Code: ErrorProviderInternal, Message: "provider returned an invalid structured error", Retryable: false}
		}
		return writeError(output, codec, request, *providerErr)
	}
	response := Response{
		ProtocolVersion: Version,
		RequestID:       request.RequestID,
		Operation:       request.Operation,
		Status:          StatusOK,
		Payload:         payload,
	}
	return codec.EncodeResponse(output, response)
}

func dispatch(ctx context.Context, handler Handler, request Request) (json.RawMessage, *ProviderError) {
	switch request.Operation {
	case OperationDescribe:
		var value DescribeRequest
		if err := DecodePayload(request.Payload, &value); err != nil {
			return nil, protocolPayloadError(err)
		}
		result, providerErr := handler.Describe(ctx, value)
		return encodeHandlerResult(result, providerErr)
	case OperationPlan:
		var value PlanRequest
		if err := DecodePayload(request.Payload, &value); err != nil {
			return nil, protocolPayloadError(err)
		}
		result, providerErr := handler.Plan(ctx, value)
		return encodeHandlerResult(result, providerErr)
	case OperationApply:
		var value ApplyRequest
		if err := DecodePayload(request.Payload, &value); err != nil {
			return nil, protocolPayloadError(err)
		}
		result, providerErr := handler.Apply(ctx, value)
		return encodeHandlerResult(result, providerErr)
	case OperationReconcile:
		var value ReconcileRequest
		if err := DecodePayload(request.Payload, &value); err != nil {
			return nil, protocolPayloadError(err)
		}
		result, providerErr := handler.Reconcile(ctx, value)
		return encodeHandlerResult(result, providerErr)
	default:
		return nil, &ProviderError{Code: ErrorProtocol, Message: "unsupported operation", Retryable: false}
	}
}

func encodeHandlerResult[T any](value T, providerErr *ProviderError) (json.RawMessage, *ProviderError) {
	if providerErr != nil {
		return nil, providerErr
	}
	payload, err := EncodePayload(value)
	if err != nil {
		return nil, &ProviderError{Code: ErrorProviderInternal, Message: "provider returned an invalid response", Retryable: false}
	}
	return payload, nil
}

func writeError(output io.Writer, codec Codec, request Request, providerErr ProviderError) error {
	response := Response{
		ProtocolVersion: Version,
		RequestID:       request.RequestID,
		Operation:       request.Operation,
		Status:          StatusError,
		Error:           &providerErr,
	}
	return codec.EncodeResponse(output, response)
}

func protocolPayloadError(err error) *ProviderError {
	return &ProviderError{Code: ErrorProtocol, Message: "invalid operation payload: " + err.Error(), Retryable: false}
}
