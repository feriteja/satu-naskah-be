# SatuNaskah Backend

This is the backend service for **SatuNaskah**, a real-time collaborative document editing platform. It is built with Go and utilizes WebSockets for real-time communication and PostgreSQL for data persistence.

## Features

- **Real-time Collaboration**: Multiple users can edit documents simultaneously.
- **Presence System**: See who is currently viewing the document and their cursor positions.
- **Comments**: Add, resolve, and delete comments on documents.
- **Role-Based Access Control**: Granular permissions (Owner, Writer, Reviewer, Reader).
- **Authentication**: Integrated with Supabase Auth (JWT).
- **Structured Logging**: High-performance logging using Uber's Zap.

## Tech Stack

- **Language**: Go (Golang)
- **WebSockets**: `github.com/gorilla/websocket`
- **Database**: PostgreSQL (via `database/sql` and `lib/pq`)
- **Authentication**: Supabase (JWT validation)
- **Logging**: `go.uber.org/zap`
- **Configuration**: `github.com/joho/godotenv`

## Prerequisites

- Go 1.21+
- PostgreSQL Database (or Supabase project)

## Setup & Installation

1. **Clone the repository**

   ```bash
   git clone <repository-url>
   cd satu-naskah-be
   ```

2. **Configure Environment Variables**
   Create a `.env` file in the root directory with the following variables:

   ```env
   # Database Configuration
   user=your_db_user
   password=your_db_password
   host=your_db_host
   port=5432
   dbname=your_db_name

   # Supabase Configuration
   SUPABASE_URL=https://your-project.supabase.co
   SUPABASE_JWT_SECRET=your_supabase_jwt_secret
   ```

3. **Install Dependencies**

   ```bash
   go mod tidy
   ```

4. **Run the Server**
   ```bash
   go run main.go
   ```
   The server will start on port `:8080`.

## Database Setup

Run the following SQL commands in your Supabase SQL Editor to create the required tables:

```sql
-- Documents Table
create table documents (
  id text primary key,
  title text not null default 'Untitled Document',
  content text default '{"ops":[]}',
  owner_id uuid references auth.users(id) not null,
  updated_at timestamp with time zone default now(),
  created_at timestamp with time zone default now()
);

-- Collaborators Table
create table collaborators (
  document_id text references documents(id) on delete cascade,
  user_id uuid references auth.users(id) not null,
  role text check (role in ('writer', 'reviewer', 'reader')),
  primary key (document_id, user_id)
);

-- Comments Table
create table comments (
  id uuid primary key default gen_random_uuid(),
  document_id text references documents(id) on delete cascade,
  user_id uuid references auth.users(id) not null,
  content text not null,
  quote text,
  text_range text,
  is_resolved boolean default false,
  created_at timestamp with time zone default now()
);
```

## API Endpoints

### Documents

- `POST /documents` - Create a new document.
- `GET /documents` - List user's documents.
- `POST /documents/save` - Save document content.
- `PUT /documents?docId={id}` - Update document title.
- `DELETE /documents?docId={id}` - Delete a document.
- `GET /documents/members?docId={id}` - Get document collaborators.
- `POST /documents/collaborator` - Invite a collaborator.

### Comments

- `GET /comments?docId={id}` - Get comments for a document.
- `POST /comments` - Add a comment.
- `PUT /comments/resolve?commentId={id}` - Resolve/Unresolve a comment.
- `DELETE /comments?commentId={id}` - Delete a comment.

## WebSocket API

Connect to the WebSocket endpoint to enable real-time features.

**URL**: `ws://localhost:8080/ws?docId={docId}&token={jwt_token}`

The WebSocket handles `UPDATE` (content changes), `CURSOR` (user positions), `PRESENCE_UPDATE`, and `COMMENT` events.
