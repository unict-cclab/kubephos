-- An administrator may answer a request once, when resolving it. Reopening clears the answer.
ALTER TABLE admin_requests
    ADD COLUMN reply text CHECK (length(reply) BETWEEN 1 AND 4000),
    ADD CHECK (reply IS NULL OR status = 'resolved');
