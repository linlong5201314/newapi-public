import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/lib/api";

import { selectedPassingModels } from "../api";
import { ModelTestButton } from "../model-test-button";
import type { ModelProbeBatch, ModelProbeResult } from "../types";

vi.mock("@/lib/api", () => ({ api: { get: vi.fn(), post: vi.fn() } }));
let client: QueryClient;
const now = Math.floor(Date.now() / 1000);
function sample(model: string, status: ModelProbeResult["status"] = "available"): ModelProbeResult {
  return {
    channel_id: 7,
    model,
    endpoint_id: 42,
    subscription_id: 5,
    status,
    reason: status === "available" ? "passed" : "rate_limited",
    http_status: 200,
    latency_ms: 20,
    checked_at: now,
    expires_at: now + 600,
    first_checked_at: now,
    last_available_at: now,
    lifetime_samples: 1,
    lifetime_passes: 1,
    lifetime_failures: 0,
    recent_sample_count: 1,
    recent_passes: 1,
    recent_failures: 0,
  };
}
function renderTest(props: Partial<React.ComponentProps<typeof ModelTestButton>> = {}) {
  return render(
    <QueryClientProvider client={client}>
      <ModelTestButton channelId={7} models={["alpha", "beta", "gamma", "delta"]} {...props} />
    </QueryClientProvider>,
  );
}
beforeEach(() => {
  vi.resetAllMocks();
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        channel_id: 7,
        endpoints: [
          { id: 0, status: "unknown" },
          { id: 42, subscription_id: 5, public: true, status: "unknown" },
        ],
        results: [sample("alpha", "unknown")],
        endpoint_has_more: false,
        max_models: 3,
        max_endpoints: 3,
        max_output_tokens: 8,
      },
    },
  });
});
afterEach(() => {
  client.clear();
});

describe("explicit bounded model tests", () => {
  it("opening the dialog reads safe history but never sends inference", async () => {
    const user = userEvent.setup();
    renderTest();
    expect(api.get).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Test models" }));
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByText("Subscription #5");
    expect(api.post).not.toHaveBeenCalled();
    expect(within(dialog).getByRole("checkbox", { name: "delta" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    expect(within(dialog).getByRole("checkbox", { name: "alpha" })).toBeChecked();
    expect(within(dialog).getByText("Unknown")).toBeVisible();
  });

  it("only an explicitly selected fresh passing model reaches the caller", async () => {
    const user = userEvent.setup();
    const onPassed = vi.fn();
    vi.mocked(api.post).mockResolvedValue({
      data: {
        success: true,
        data: {
          channel_id: 7,
          passed_models: ["alpha"],
          results: [sample("alpha"), sample("beta", "unknown"), sample("gamma", "unavailable")],
        },
      },
    });
    renderTest({ onPassedModels: onPassed, endpointIds: [42] });
    await user.click(screen.getByRole("button", { name: "Test models" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(within(dialog).getByRole("button", { name: "Run selected tests" })).toBeEnabled(),
    );
    await user.click(within(dialog).getByRole("button", { name: "Run selected tests" }));
    await within(dialog).findByRole("checkbox", { name: "Use alpha" });
    expect(onPassed).not.toHaveBeenCalled();
    expect(within(dialog).queryByRole("checkbox", { name: "Use beta" })).not.toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Use selected passing models" }),
    ).toBeDisabled();
    await user.click(within(dialog).getByRole("checkbox", { name: "Use alpha" }));
    await user.click(within(dialog).getByRole("button", { name: "Use selected passing models" }));
    expect(onPassed).toHaveBeenCalledWith(["alpha"]);
    expect(api.post).toHaveBeenCalledWith(
      "/api/channel/self/model_probe/7",
      { models: ["alpha", "beta", "gamma"], endpoint_ids: [42] },
      expect.objectContaining({ signal: expect.any(AbortSignal), timeout: 130000 }),
    );
  });

  it("cancel aborts the pending request and never offers unfinished models", async () => {
    const user = userEvent.setup();
    let signal: AbortSignal | undefined;
    vi.mocked(api.post).mockImplementation(
      (_url, _body, config) =>
        new Promise((_resolve, reject) => {
          signal = config?.signal as AbortSignal;
          signal.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")));
        }),
    );
    renderTest({ models: ["alpha"], onPassedModels: vi.fn() });
    await user.click(screen.getByRole("button", { name: "Test models" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(within(dialog).getByRole("button", { name: "Run selected tests" })).toBeEnabled(),
    );
    await user.click(within(dialog).getByRole("button", { name: "Run selected tests" }));
    await user.click(await within(dialog).findByRole("button", { name: "Cancel test" }));
    expect(signal?.aborted).toBe(true);
    await within(dialog).findByText("Test cancelled. Unfinished samples remain unknown.");
    expect(within(dialog).queryByRole("checkbox", { name: "Use alpha" })).not.toBeInTheDocument();
  });

  it("a failed request remains an error without enabling any model", async () => {
    vi.mocked(api.post).mockResolvedValue({
      data: { success: false, message: "Selected endpoint is unavailable or not permitted" },
    });
    const user = userEvent.setup();
    renderTest({ models: ["alpha"], onPassedModels: vi.fn() });
    await user.click(screen.getByRole("button", { name: "Test models" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(within(dialog).getByRole("button", { name: "Run selected tests" })).toBeEnabled(),
    );
    await user.click(within(dialog).getByRole("button", { name: "Run selected tests" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Selected endpoint is unavailable or not permitted",
    );
    expect(
      within(dialog).getByRole("button", { name: "Use selected passing models" }),
    ).toBeDisabled();
  });

  it("account-bound egress rejects arbitrary preset node tests honestly", async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        success: true,
        data: { channel_id: 7, endpoints: [], results: [], configured_egress_only: true },
      },
    });
    const user = userEvent.setup();
    renderTest({ endpointIds: [42] });
    await user.click(screen.getByRole("button", { name: "Test models" }));
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByText("Account model tests must use configured egress");
    expect(within(dialog).getByRole("button", { name: "Run selected tests" })).toBeDisabled();
    expect(api.post).not.toHaveBeenCalled();
  });

  it("keyboard activation opens the dialog and missing persisted channel disables test", async () => {
    const view = renderTest({ channelId: 0 });
    expect(screen.getByRole("button", { name: "Test models" })).toBeDisabled();
    view.unmount();
    renderTest({ models: ["alpha"] });
    const user = userEvent.setup();
    screen.getByRole("button", { name: "Test models" }).focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("dialog")).toBeVisible();
  });
});

describe("passing selection contract", () => {
  it("rejects stale, unknown, missing and unselected successes", () => {
    const batch: ModelProbeBatch = {
      channel_id: 7,
      passed_models: ["alpha", "beta", "gamma", "delta"],
      results: [
        sample("alpha"),
        { ...sample("beta"), expires_at: now - 1 },
        sample("gamma", "unknown"),
        sample("delta"),
      ],
    };
    expect(selectedPassingModels(batch, ["alpha", "beta", "gamma"], now * 1000)).toEqual(["alpha"]);
  });
});
