// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package secret

import (
	"net/url"
	"strings"
)

// RedactDataSourcePassword returns a copy of the given database connection
// string with the password replaced by "REDACTED", so it is safe to log.
func RedactDataSourcePassword(dataSource *string) {
	if dataSource == nil || *dataSource == "" {
		return
	}

	// URL-style connection strings (e.g. postgres://user:pass@host/db).
	if u, err := url.Parse(*dataSource); err == nil && u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "REDACTED")
			*dataSource = u.String()
		}
		return
	}

	// mysql-style DSN: user:pass@tcp(host:port)/db.
	if idx := strings.LastIndex(*dataSource, "@"); idx > 0 {
		creds := (*dataSource)[:idx]
		if before, _, ok := strings.Cut(creds, ":"); ok {
			*dataSource = before + ":REDACTED" + (*dataSource)[idx:]
		}
	}
}
