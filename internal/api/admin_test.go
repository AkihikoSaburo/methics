package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dash.algorithmics/internal/store"
)

// Validasi CreateLecturer yang terjadi sebelum menyentuh MongoDB.
func TestCreateLecturerValidation(t *testing.T) {
	old := store.MinPasswordLength
	store.MinPasswordLength = 12
	defer func() { store.MinPasswordLength = old }()
	s := New(nil, Config{})
	cases := map[string]struct {
		in   LecturerInput
		want error
	}{
		"kosong":          {LecturerInput{}, ErrMissingFields},
		"username spasi":  {LecturerInput{Username: "rina wibowo", DisplayName: "Rina", Password: strings.Repeat("x", 12), ClassIDs: []string{"IK24-A"}}, ErrInvalidUsername},
		"username pendek": {LecturerInput{Username: "ab", DisplayName: "Rina", Password: strings.Repeat("x", 12), ClassIDs: []string{"IK24-A"}}, ErrInvalidUsername},
		"password pendek": {LecturerInput{Username: "rina", DisplayName: "Rina", Password: "pendek", ClassIDs: []string{"IK24-A"}}, ErrPasswordTooShort},
		"tanpa kelas":     {LecturerInput{Username: "rina", DisplayName: "Rina", Password: strings.Repeat("x", 12), ClassIDs: []string{" ", ""}}, ErrNoClasses},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.CreateLecturer(context.Background(), tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if !usernamePattern.MatchString("rina.wibowo") || usernamePattern.MatchString("Rina") {
		t.Fatal("username pattern")
	}
	if msg := LecturerErrorMessage(ErrPasswordTooShort); !strings.Contains(msg, "12") {
		t.Fatal(msg)
	}
}
