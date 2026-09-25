/*
 * Copyright Metaplay. Licensed under the Apache-2.0 license.
 */

package auth

import (
	"os"
	"time"

	"github.com/rs/zerolog/log"

	clierrors "github.com/metaplay/cli/internal/errors"
)

// sessionLockTimeout is how long to wait for another process to release the
// session store. It outlasts a token refresh, which ends within refreshTimeout.
var sessionLockTimeout = refreshTimeout + 10*time.Second

// sessionLockPollInterval is how often a waiting process tries the lock again.
const sessionLockPollInterval = 25 * time.Millisecond

// lockSessionStore takes the lock every access to config.json holds, and
// returns the function that releases it. Several CLI processes use the store
// at once, kubectl's credential plugin among them, and a refresh token
// presented twice is taken for a stolen one: the auth server revokes the whole
// session. Holding the lock from reading a session to saving its refresh means
// a second process finds the refreshed tokens rather than refreshing again.
//
// The lock is an OS lock on a file of its own, so it is released if its holder
// dies, and it excludes other open files within one process too. Where no lock
// can be had at all, such as a read-only config directory or a filesystem
// without locks, the store is used unlocked, as it was before the lock existed.
func lockSessionStore() (unlock func(), err error) {
	configPath, err := resolvePersistedConfigFilePath()
	if err != nil {
		return nil, err
	}
	lockPath := configPath + ".lock"

	// Opened for reading only, which is all a lock needs, so that a lock file
	// another user created, such as root under sudo, still serves this one.
	file, err := os.OpenFile(lockPath, os.O_RDONLY|os.O_CREATE, 0644)
	if err != nil {
		if cannotCreateLockFile(err) {
			log.Debug().Msgf("Using the session store unlocked, as %s cannot be created: %v", lockPath, err)
			return func() {}, nil
		}
		return nil, clierrors.Wrapf(err, "Failed to open the session lock %s", lockPath)
	}

	deadline := time.Now().Add(sessionLockTimeout)
	for {
		locked, err := tryLockFile(file)
		if err != nil {
			_ = file.Close()
			if lockUnsupported(err) {
				log.Debug().Msgf("Using the session store unlocked, as %s cannot be locked: %v", lockPath, err)
				return func() {}, nil
			}
			return nil, clierrors.Wrapf(err, "Failed to lock the session store %s", lockPath)
		}
		if locked {
			break
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, clierrors.Newf("Timed out after %v waiting for another metaplay process to release the session store", sessionLockTimeout).
				WithDetails("Lock file: " + lockPath).
				WithSuggestion("Try again, and check for a 'metaplay' process that has not exited")
		}
		time.Sleep(sessionLockPollInterval)
	}

	return func() {
		// Closing the file releases the lock as well, but say so explicitly.
		_ = unlockFile(file)
		_ = file.Close()
	}, nil
}
