package cloud

import (
	"context"
	"time"
)

// LoginError is a device login the browser refused or let expire.
type LoginError struct{ Code string }

func (e *LoginError) Error() string {
	if e.Code == "access_denied" {
		return "the login was refused in the browser"
	}
	return "the login code expired before it was approved: run 'clai cloud login' again"
}

// LoginFlow is the device authorization grant's client side, with its
// clock and wait injected.
type LoginFlow struct {
	Client *Client
	Now    func() time.Time
	Sleep  func(ctx context.Context, d time.Duration) error
	// Show prints the code and where to approve it, once.
	Show func(DeviceCode) error
}

// Run starts a login for a device called name and waits until the browser
// approves or refuses it, or the code expires.
func (f LoginFlow) Run(ctx context.Context, name string) (DeviceToken, error) {
	code, err := f.Client.StartDevice(ctx, name)
	if err != nil {
		return DeviceToken{}, err
	}
	if err := f.Show(code); err != nil {
		return DeviceToken{}, err
	}
	interval := time.Duration(max(code.Interval, 1)) * time.Second
	deadline := f.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	for {
		if err := f.Sleep(ctx, interval); err != nil {
			return DeviceToken{}, err
		}
		tok, err := f.Client.PollDevice(ctx, code.DeviceCode)
		if err == nil {
			return tok, nil
		}
		apiErr, ok := AsAPIError(err)
		if !ok {
			return DeviceToken{}, err
		}
		switch apiErr.Code {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied", "expired_token":
			return DeviceToken{}, &LoginError{Code: apiErr.Code}
		default:
			return DeviceToken{}, err
		}
		if f.Now().After(deadline) {
			return DeviceToken{}, &LoginError{Code: "expired_token"}
		}
	}
}
