-- =============================================================================
-- sqlc queries for the user-service
-- Run `sqlc generate` from services/user-service/ to regenerate Go code.
--
-- Naming convention:
--   :one   → returns a single row  (sql.ErrNoRows if not found)
--   :exec  → returns no rows       (only checks for execution error)
--   :many  → returns []Row
-- =============================================================================

-- ─── User creation ────────────────────────────────────────────────────────────

-- name: CreateUser :one
-- Inserts a new base user row. password_hash and salt_password start empty;
-- they are populated when the employee activates their account (Issue 11).
INSERT INTO users (
    email,
    password_hash,
    salt_password,
    user_type,
    first_name,
    last_name,
    birth_date,
    gender,
    phone_number,
    address,
    is_active
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING id, email, user_type, first_name, last_name, is_active, created_at;

-- name: CreateEmployeeDetails :exec
-- Inserts the employee-specific extension row in employee_details.
-- Must be called after CreateUser within the same transaction.
INSERT INTO employee_details (user_id, username, position, department)
VALUES ($1, $2, $3, $4);

-- ─── Employee listing (Issue 6) ───────────────────────────────────────────────

-- name: ListEmployees :many
-- Returns employees matching the optional filters, with pagination.
-- Pass NULL for any filter to skip it; the handler maps "" → nil for that purpose.
-- Joins employee_details so only users with an employee profile are returned.
SELECT
    u.id,
    u.email,
    u.first_name,
    u.last_name,
    u.user_type,
    u.is_active,
    u.phone_number,
    e.position,
    e.department
FROM users u
INNER JOIN employee_details e ON e.user_id = u.id
WHERE
    (sqlc.narg('email')::text      IS NULL OR u.email      ILIKE '%' || sqlc.narg('email')::text      || '%')
AND (sqlc.narg('first_name')::text IS NULL OR u.first_name ILIKE '%' || sqlc.narg('first_name')::text || '%')
AND (sqlc.narg('last_name')::text  IS NULL OR u.last_name  ILIKE '%' || sqlc.narg('last_name')::text  || '%')
AND (sqlc.narg('position')::text   IS NULL OR e.position   ILIKE '%' || sqlc.narg('position')::text   || '%')
ORDER BY u.id ASC
LIMIT $1 OFFSET $2;

-- ─── Employee detail lookup (Issue 7) ────────────────────────────────────────

-- name: GetEmployeeByID :one
-- Full employee profile for the edit form: users JOIN employee_details.
-- Returns sql.ErrNoRows when the user does not exist or has no employee_details row.
SELECT
    u.id,
    u.email,
    u.first_name,
    u.last_name,
    u.birth_date,
    u.gender,
    u.phone_number,
    u.address,
    u.user_type,
    u.is_active,
    u.created_at,
    e.username,
    e.position,
    e.department
FROM users u
INNER JOIN employee_details e ON e.user_id = u.id
WHERE u.id = $1
LIMIT 1;

-- ─── Employee update (Issue 8) ───────────────────────────────────────────────

-- name: UpdateUser :exec
-- Updates the mutable base fields of an existing user row.
-- Called inside a transaction by UpdateEmployee; never updates password_hash/salt.
UPDATE users
SET email        = $2,
    first_name   = $3,
    last_name    = $4,
    birth_date   = $5,
    gender       = $6,
    phone_number = $7,
    address      = $8,
    is_active    = $9
WHERE id = $1;

-- name: UpdateUserActive :exec
-- Updates only is_active for a user. Used by ToggleEmployeeActive for any employee/admin.
UPDATE users SET is_active = $2 WHERE id = $1;

-- name: UpdateEmployeeDetails :exec
-- Updates position and department for an existing employee_details row.
UPDATE employee_details
SET position   = $2,
    department = $3
WHERE user_id = $1;

-- name: DeleteUserPermissions :exec
-- Removes all permission assignments for a user.
-- Called before re-assigning the full permission set in UpdateEmployee.
DELETE FROM user_permissions WHERE user_id = $1;

-- ─── Client search (Autocomplete / Infinite Scroll) ──────────────────────────

-- name: SearchClients :many
-- Returns clients matching an optional query string against first_name, last_name,
-- or email. Ordered by created_at DESC (newest first) with LIMIT/OFFSET pagination.
-- Pass NULL for query to skip the filter and return all clients.
-- The caller should request limit+1 rows to detect has_more without a COUNT query.
SELECT
    u.id,
    u.first_name,
    u.last_name,
    u.email
FROM users u
WHERE u.user_type = 'CLIENT'
AND (sqlc.narg('query')::text IS NULL
     OR u.first_name ILIKE '%' || sqlc.narg('query')::text || '%'
     OR u.last_name  ILIKE '%' || sqlc.narg('query')::text || '%'
     OR u.email      ILIKE '%' || sqlc.narg('query')::text || '%')
ORDER BY u.created_at DESC
LIMIT $1 OFFSET $2;

-- ─── Authentication ───────────────────────────────────────────────────────────

-- name: GetUserByEmail :one
-- Full row including password_hash and salt_password.
-- ONLY used by the Login handler to verify credentials — never returned to the caller.
SELECT
    id,
    email,
    password_hash,
    salt_password,
    user_type,
    first_name,
    last_name,
    birth_date,
    gender,
    phone_number,
    address,
    is_active,
    created_at
FROM users
WHERE email = $1
LIMIT 1;

-- ─── Profile lookup ───────────────────────────────────────────────────────────

-- name: GetUserByID :one
-- Safe projection — excludes password_hash and salt_password.
-- Used by GetEmployee, token refresh, and internal service calls.
SELECT
    id,
    email,
    user_type,
    first_name,
    last_name,
    birth_date,
    gender,
    phone_number,
    address,
    is_active,
    created_at
FROM users
WHERE id = $1
LIMIT 1;

-- ─── Permission codebook (Issue 5) ───────────────────────────────────────────

-- name: GetAllPermissions :many
-- Returns the complete permission codebook, ordered by id.
-- Used by the admin frontend to populate Create/Edit Employee permission lists.
SELECT id, permission_code FROM permissions ORDER BY id ASC;

-- ─── Permissions (required for CreateEmployee and Login JWT) ──────────────────

-- name: AssignUserPermission :exec
-- Links a permission to an employee via the user_permissions junction table.
-- Call once per permission code when creating an employee (Issue 9).
INSERT INTO user_permissions (user_id, permission_id)
SELECT $1, id
FROM permissions
WHERE permission_code = $2
ON CONFLICT DO NOTHING;

-- name: GetUserPermissions :many
-- Returns all permission codes held by a user.
-- Used during Login to populate the `permissions` claim in the JWT (Issue 4).
SELECT p.permission_code
FROM permissions p
INNER JOIN user_permissions up ON up.permission_id = p.id
WHERE up.user_id = $1
ORDER BY p.permission_code;

-- ─── Account activation / password reset (Issues 10, 11, 12) ─────────────────

-- name: CreateVerificationToken :one
-- Stores a new single-use token for account activation or password reset.
INSERT INTO verification_tokens (user_id, token, token_type, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id, token, expires_at;

-- name: GetVerificationToken :one
-- Fetches a token row for validation. The caller must check:
--   - used_at IS NULL     (not yet consumed)
--   - expires_at > NOW()  (not expired)
SELECT id, user_id, token, token_type, expires_at, used_at
FROM verification_tokens
WHERE token = $1
LIMIT 1;

-- name: MarkVerificationTokenUsed :exec
-- Marks the token as consumed so it cannot be replayed (Issue 11 edge case).
UPDATE verification_tokens
SET used_at = NOW()
WHERE id = $1;

-- ─── Account activation / set password (Issue 11) ───────────────────────────

-- name: UpdateUserPassword :exec
-- Sets the password hash for the user identified by email.
-- Called by SetPassword after the activation token is verified.
UPDATE users SET password_hash = $1 WHERE email = $2;

-- ─── Password update ──────────────────────────────────────────────────────────

-- name: SetUserPassword :exec
-- Writes the hashed password and salt after successful activation or reset.
-- Only called from the ActivateAccount and SetNewPassword handlers.
UPDATE users
SET password_hash = $2,
    salt_password = $3,
    is_active     = TRUE
WHERE id = $1;
