package codexmanager

import (
	"context"
	"net/http"
)

type RPCService struct {
	client RPCClientAPI
}

func NewRPCService(client RPCClientAPI) *RPCService {
	return &RPCService{client: client}
}

func (s *RPCService) ListAccounts(ctx context.Context, params RPCAccountListParams) (RPCAccountListResult, error) {
	client, err := s.requireClient()
	if err != nil {
		return RPCAccountListResult{}, err
	}
	return client.ListAccounts(ctx, params)
}

func (s *RPCService) ImportAccounts(ctx context.Context, contents []string) (RPCAccountImportResult, error) {
	client, err := s.requireClient()
	if err != nil {
		return RPCAccountImportResult{}, err
	}
	return client.ImportAccounts(ctx, contents)
}

func (s *RPCService) DeleteAccount(ctx context.Context, accountID string) error {
	client, err := s.requireClient()
	if err != nil {
		return err
	}
	return client.DeleteAccount(ctx, accountID)
}

func (s *RPCService) StartLogin(ctx context.Context, req RPCLoginStartRequest) (RPCLoginStartResult, error) {
	client, err := s.requireClient()
	if err != nil {
		return RPCLoginStartResult{}, err
	}
	return client.StartLogin(ctx, req)
}

func (s *RPCService) GetLoginStatus(ctx context.Context, loginID string) (RPCLoginStatusResult, error) {
	client, err := s.requireClient()
	if err != nil {
		return RPCLoginStatusResult{}, err
	}
	return client.GetLoginStatus(ctx, loginID)
}

func (s *RPCService) CompleteLogin(ctx context.Context, req RPCLoginCompleteRequest) error {
	client, err := s.requireClient()
	if err != nil {
		return err
	}
	return client.CompleteLogin(ctx, req)
}

func (s *RPCService) ReadUsage(ctx context.Context, accountID string) (RPCUsageReadResult, error) {
	client, err := s.requireClient()
	if err != nil {
		return RPCUsageReadResult{}, err
	}
	return client.ReadUsage(ctx, accountID)
}

func (s *RPCService) ListUsage(ctx context.Context) (RPCUsageListResult, error) {
	client, err := s.requireClient()
	if err != nil {
		return RPCUsageListResult{}, err
	}
	return client.ListUsage(ctx)
}

func (s *RPCService) RefreshUsage(ctx context.Context, accountID string) error {
	client, err := s.requireClient()
	if err != nil {
		return err
	}
	return client.RefreshUsage(ctx, accountID)
}

func (s *RPCService) requireClient() (RPCClientAPI, error) {
	if s == nil || s.client == nil {
		return nil, NewCodedError(http.StatusInternalServerError, CodeInternalError, "codex-manager rpc service is not initialized", false)
	}
	return s.client, nil
}
