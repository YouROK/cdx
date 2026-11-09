function setup(cdx) {
  // Fixed choices become options with stable ids; the answer is returned as a label.
  function optionList(raw) {
    var list = [];
    if (!raw || !raw.length) return list;
    for (var i = 0; i < raw.length && list.length < 20; i++) {
      var text = String(raw[i] || "").trim();
      if (!text) continue;
      list.push({ id: "o" + (list.length + 1), label: text });
    }
    return list;
  }

  cdx.tools.register({
    name: "ask_user",
    description: "Ask the user a question and wait for the answer. Use it when the request is ambiguous and the answer changes the work; do not ask for permission, the system handles that. Returns the chosen option or the typed answer, or a cancelled flag when no interactive client is attached.",
    permission: "read",
    input_schema: {
      type: "object",
      properties: {
        question: { type: "string", description: "The question to show the user" },
        options: {
          type: "array",
          description: "Optional fixed choices shown as a list",
          items: { type: "string" }
        },
        allow_text: {
          type: "boolean",
          description: "Allow a free-text answer in addition to the choices",
          default: true
        }
      },
      required: ["question"]
    }
  }, cdx.inWorker(function(args) {
    var title = String(args.question || "").trim();
    if (!title) return { error: "question is required" };

    var options = optionList(args.options);
    var allowText = args.allow_text !== false || options.length === 0;
    var answer = cdx.questions.ask({
      kind: "model",
      title: title,
      options: options,
      text: allowText
    });

    if (answer.cancelled) {
      return { cancelled: true, reason: answer.reason || "no answer" };
    }
    if (answer.text) {
      return { answer: answer.text };
    }
    for (var i = 0; i < options.length; i++) {
      if (options[i].id === answer.choice) {
        return { answer: options[i].label };
      }
    }
    return { answer: answer.choice };
  }));
}
