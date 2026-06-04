-- Comms module — DB-backed message templates (Phase 0).
--
-- Templates are looked up by (channel, template_id, locale). The Renderer
-- resolves locale with fallback to the default locale ('en') when an exact
-- locale match is absent. Body is required; subject is NULL for channels that
-- have no subject concept (SMS / PUSH / INAPP / LINE).
--
-- NO FOREIGN KEY constraints platform-wide (CONVENTIONS §11 / CLAUDE.md #9).
-- This table is configuration data (not observability) so it has NO TTL.

CREATE TABLE comms_templates (
    channel     text        NOT NULL CHECK (channel IN ('EMAIL', 'SMS', 'PUSH', 'INAPP', 'LINE')),
    template_id text        NOT NULL,
    locale      text        NOT NULL DEFAULT 'en',
    subject     text,                          -- NULL for subject-less channels
    body        text        NOT NULL,
    version     int         NOT NULL DEFAULT 1,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (channel, template_id, locale)
);
