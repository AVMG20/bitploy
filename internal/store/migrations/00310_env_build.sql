-- +goose Up
-- Build-time variables (docs/decisions.md, "Build-time variables are app
-- variables with a build flag"). An app variable can also be handed to the
-- image build:
--  build  ''      only when the app runs (every variable before this)
--         public  also a --build-arg, and in bitploy's own Node templates an
--                 ARG before the build script, so Vite and Next.js bake it in
--         secret  also a BuildKit secret (--secret id=NAME), never a build arg
--                 and never in the image or its history
ALTER TABLE env_vars ADD COLUMN build TEXT NOT NULL DEFAULT '' CHECK (build IN ('', 'public', 'secret'));

-- +goose Down
ALTER TABLE env_vars DROP COLUMN build;
