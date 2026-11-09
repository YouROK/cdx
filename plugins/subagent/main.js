function setup(cdx) {
  cdx.tools.register({
    name: "subagent",
    description: "Delegate a self-contained task to a child agent. The child does not see this conversation: include the goal, relevant files or paths, constraints, and the expected result in the prompt. The child works in the same directory with the same tools and returns a concise final answer. Use it for focused work that would otherwise fill this context.",
    permission: "read",
    input_schema: {
      type: "object",
      properties: {
        prompt: {
          type: "string",
          description: "Self-contained task brief: goal, relevant files, constraints, and expected result"
        }
      },
      required: ["prompt"]
    }
  }, cdx.inWorker(function(args) {
    var prompt = String(args.prompt || "").trim();
    if (!prompt) return { error: "prompt is required" };

    var result = cdx.agents.run({
      prompt: prompt,
      preset: "subagent"
    });
    return {
      content: result.content,
      session_id: result.session_id,
      steps: result.steps,
      stopped_reason: result.stopped_reason
    };
  }));
}
