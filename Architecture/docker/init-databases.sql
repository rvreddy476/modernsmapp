-- Create additional databases needed by services
-- (Postgres auto-creates 'app' via POSTGRES_DB env var)

SELECT 'CREATE DATABASE identity_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'identity_db')\gexec

SELECT 'CREATE DATABASE chat_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'chat_db')\gexec
