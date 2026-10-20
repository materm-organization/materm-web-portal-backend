package validator

import "errors"

// ValidateLaoPhone validates a Lao phone number starting with 020.
func ValidateLaoPhone(phone string) error {
	if phone == "" {
		return errors.New("please provide a phone number")
	}
	if len(phone) < 3 || phone[0:3] != "020" {
		return errors.New("please enter 020 first")
	}
	if len(phone) != 11 {
		return errors.New("must be 11 digits")
	}
	switch phone[3:4] {
	case "9", "7", "5", "2", "8":
		return nil
	default:
		return errors.New("invalid Laos number prefix")
	}
}
