-- Messages members send to administrators. Administrators mark them resolved;
-- members see the outcome. Requests belong to users and are not part of state transfers.
CREATE TABLE admin_requests (
    id text PRIMARY KEY,
    requester_user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message text NOT NULL CHECK (length(message) BETWEEN 1 AND 4000),
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    resolved_by_user_id text REFERENCES users(id) ON DELETE SET NULL,
    resolved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'resolved') = (resolved_at IS NOT NULL))
);
CREATE INDEX admin_requests_status ON admin_requests(status, created_at DESC);
CREATE INDEX admin_requests_requester ON admin_requests(requester_user_id, created_at DESC);
