function setup(cdx) {
  // Keep only non-empty briefs, up to the batch limit.
  function taskList(raw) {
    var out = [];
    if (!raw || !raw.length) return out;
    for (var i = 0; i < raw.length && out.length < 8; i++) {
      var text = String(raw[i] || "").trim();
      if (text) out.push(text);
    }
    return out;
  }

  cdx.tools.register({
    name: "subagent",
    description: "Delegate one task, or up to 8 independent tasks in parallel, to child agents. Each child does not see this conversation: include the goal, relevant files or paths, constraints, and the expected result in every brief. The children work in the same directory with the same tools and return concise final answers.",
    permission: "read",
    input_schema: {
      type: "object",
      properties: {
        prompt: { type: "string", description: "Self-contained brief for one child" },
        tasks: {
          type: "array",
          description: "Up to 8 independent briefs; they run in parallel",
          items: { type: "string" }
        }
      }
    }
  }, cdx.inWorker(function(args) {
    var tasks = taskList(args.tasks);
    if (tasks.length > 0) {
      var batch = cdx.agents.runMany({ prompts: tasks, preset: "subagent" });
      return { results: batch.results };
    }

    var prompt = String(args.prompt || "").trim();
    if (!prompt) return { error: "prompt or tasks are required" };

    var result = cdx.agents.run({ prompt: prompt, preset: "subagent" });
    return {
      content: result.content,
      session_id: result.session_id,
      steps: result.steps,
      stopped_reason: result.stopped_reason
    };
  }));
}
