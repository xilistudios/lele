package keyring

import "errors"

const systemScope = "system"

var errNilService = errors.New("keyring: nil service")

type OpaqueVault struct {
	svc    *Service
	source string
}

func NewOpaqueVault(svc *Service, source string) *OpaqueVault {
	return &OpaqueVault{svc: svc, source: source}
}

func (v *OpaqueVault) GetSecret(name string) (string, error) {
	if v == nil || v.svc == nil {
		return "", errNilService
	}
	val, err := v.svc.GetRaw(name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return val, nil
}

func (v *OpaqueVault) SetSecret(name, value string) error {
	if v == nil || v.svc == nil {
		return errNilService
	}
	return v.svc.SetFromUI(name, value, v.description(), nil, []string{systemScope}, v.source)
}

func (v *OpaqueVault) DeleteSecret(name string) error {
	if v == nil || v.svc == nil {
		return errNilService
	}
	err := v.svc.DeleteFromUI(name, v.source)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

func (v *OpaqueVault) description() string {
	if v.source == "" {
		return "Internal secret"
	}
	return "Stored by " + v.source
}
