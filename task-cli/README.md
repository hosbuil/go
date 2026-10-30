# Go task CLI

A task manager using only Go's standard library. Requires Go 1.20+.

From this directory:

```sh
go build -o task-cli .
./task-cli add "Buy groceries"
./task-cli update 1 "Buy groceries and cook dinner"
./task-cli mark-in-progress 1
./task-cli mark-done 1
./task-cli list
./task-cli list done
./task-cli list not-done
./task-cli list in-progress
./task-cli list todo
./task-cli delete 1
./task-cli help
```

Quote descriptions containing spaces. New tasks start as `todo`. `not-done` includes both `todo` and `in-progress`.

The first valid task command creates `tasks.json` in the current working directory. Tasks have stable IDs that are never reused, descriptions, statuses, and UTC creation/update timestamps. The JSON object contains `nextId` and a `tasks` array. Old `tasks.txt` files are not automatically imported.

Invalid inputs and storage failures produce errors and a nonzero exit code. Corrupt JSON is preserved. Changes use a temporary file and rename. Run one command at a time; concurrent writers are not supported.

```sh
go test ./...
go vet ./...
```
