function setup(cdx) {
  function workdir() {
    return cdx.workdir() || ".";
  }

  function resolvePath(path) {
    if (!path) return workdir();
    if (path.charAt(0) === "/") return path;
    return workdir().replace(/\/+$/, "") + "/" + path;
  }

  cdx.tools.register({
    name: "read_file",
    description: "Read the UTF-8 contents of a file on disk",
    permission: "read",
    input_schema: {
      type: "object",
      properties: {
        path: { type: "string", description: "Path to the file to read" }
      },
      required: ["path"]
    }
  }, cdx.inWorker(function(args) {
    return cdx.sys.readFile(resolvePath(args.path));
  }));

  cdx.tools.register({
    name: "write_file",
    description: "Write UTF-8 content to a file on disk, creating parent directories if needed",
    permission: "edit",
    input_schema: {
      type: "object",
      properties: {
        path: { type: "string", description: "Path to the file to write" },
        content: { type: "string", description: "Full content to write" }
      },
      required: ["path", "content"]
    }
  }, cdx.inWorker(function(args) {
    var path = resolvePath(args.path);
    cdx.sys.writeFile(path, args.content);
    return { status: "written", path: path };
  }));

  cdx.tools.register({
    name: "edit_file",
    description: "Replace an exact string occurrence inside an existing file",
    permission: "edit",
    input_schema: {
      type: "object",
      properties: {
        path: { type: "string", description: "Path to the file to edit" },
        old_string: { type: "string", description: "Exact text to find" },
        new_string: { type: "string", description: "Replacement text" }
      },
      required: ["path", "old_string", "new_string"]
    }
  }, cdx.inWorker(function(args) {
    var path = resolvePath(args.path);
    var content = cdx.sys.readFile(path);
    var idx = content.indexOf(args.old_string);
    if (idx === -1) {
      throw new Error("old_string not found in " + args.path);
    }
    var updated = content.slice(0, idx) + args.new_string + content.slice(idx + args.old_string.length);
    cdx.sys.writeFile(path, updated);
    return { status: "edited", path: path };
  }));

  cdx.tools.register({
    name: "list_dir",
    description: "List files and subdirectories inside a directory",
    permission: "read",
    input_schema: {
      type: "object",
      properties: {
        path: { type: "string", description: "Directory path to list" }
      },
      required: ["path"]
    }
  }, cdx.inWorker(function(args) {
    return cdx.sys.readDir(resolvePath(args.path));
  }));

  cdx.tools.register({
    name: "shell",
    description: "Execute a shell command and return stdout, stderr, and exit_code",
    permission: "dangerous",
    input_schema: {
      type: "object",
      properties: {
        command: { type: "string", description: "Shell command to execute" },
        workdir: { type: "string", description: "Optional working directory" },
        timeout_ms: { type: "integer", description: "Optional timeout in milliseconds", default: 60000 }
      },
      required: ["command"]
    }
  }, cdx.inWorker(function(args) {
    var dir = args.workdir ? resolvePath(args.workdir) : workdir();
    return cdx.sys.exec(args.command, dir, args.timeout_ms || 60000);
  }));
}
