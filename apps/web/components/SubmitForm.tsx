"use client";

import { FormEvent, useState } from "react";
import { z } from "zod";

const repositoryInput = z
  .string()
  .trim()
  .min(3)
  .refine(
    (value) =>
      /^(?:https:\/\/github\.com\/)?[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+\/?$/.test(
        value,
      ),
    "Use owner/repository or a GitHub repository URL.",
  );

type State = {
  kind: "idle" | "loading" | "success" | "error";
  message?: string;
  webURL?: string;
};

export function SubmitForm() {
  const [state, setState] = useState<State>({ kind: "idle" });

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const parsed = repositoryInput.safeParse(form.get("repository"));
    if (!parsed.success) {
      setState({ kind: "error", message: parsed.error.issues[0]?.message });
      return;
    }

    setState({ kind: "loading" });
    try {
      const response = await fetch(
        new URL(
          "/v1/repositories/track",
          process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080",
        ),
        {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Accept: "application/json",
          },
          body: JSON.stringify({ repository: parsed.data }),
          signal: AbortSignal.timeout(10_000),
        },
      );
      const body = (await response.json()) as {
        message?: string;
        webUrl?: string;
        detail?: string;
      };
      if (!response.ok)
        throw new Error(
          body.detail ?? `Request failed with ${response.status}.`,
        );
      setState({
        kind: "success",
        message: body.message ?? "Repository queued.",
        webURL: body.webUrl,
      });
    } catch (error) {
      setState({
        kind: "error",
        message:
          error instanceof Error
            ? error.message
            : "Could not queue this repository.",
      });
    }
  }

  return (
    <form className="submit-form" onSubmit={submit} noValidate>
      <label htmlFor="repository">GitHub repository</label>
      <div className="input-action">
        <input
          id="repository"
          name="repository"
          placeholder="owner/repository"
          autoComplete="off"
          required
        />
        <button className="button" disabled={state.kind === "loading"}>
          {state.kind === "loading" ? "Queuing…" : "Track repository"}
        </button>
      </div>
      <p className="field-help">
        Public repositories only. No account or write access is required.
      </p>
      {state.kind === "error" ? (
        <p className="form-message error-message">{state.message}</p>
      ) : null}
      {state.kind === "success" ? (
        <p className="form-message success-message">
          {state.message}{" "}
          {state.webURL ? <a href={state.webURL}>Open repository →</a> : null}
        </p>
      ) : null}
    </form>
  );
}
