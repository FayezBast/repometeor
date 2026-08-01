import { Command, InvalidArgumentError } from "commander";
import { Client, ClientError } from "./client.js";
import {
  formatComparison,
  formatDetail,
  formatTrack,
  formatTrending,
} from "./format.js";
import { normalizeAPIURL, normalizeRepository } from "./normalize.js";

export const version = "0.1.0";
const defaultAPI = "http://localhost:8080";

type GlobalOptions = {
  apiUrl?: string;
  json?: boolean;
  color: boolean;
  timeout: number;
};

function timeout(value: string) {
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed < 100 || parsed > 120_000)
    throw new InvalidArgumentError("must be 100–120000 milliseconds");
  return parsed;
}

function context(program: Command) {
  const options = program.opts<GlobalOptions>();
  const apiURL = normalizeAPIURL(
    options.apiUrl ?? process.env.REPOMETEOR_API_URL ?? defaultAPI,
  );
  return { options, apiURL, client: new Client(apiURL, options.timeout) };
}

function output(
  value: unknown,
  json: boolean | undefined,
  human: () => string,
) {
  process.stdout.write(
    json ? `${JSON.stringify(value, null, 2)}\n` : `${human()}\n`,
  );
}

export function buildProgram() {
  const program = new Command();
  program
    .name("repometeor")
    .description("Catch rising repositories before they hit orbit.")
    .version(version)
    .option("--api-url <url>", "RepoMeteor API URL")
    .option("--json", "write machine-readable JSON")
    .option("--no-color", "disable terminal color")
    .option("--timeout <milliseconds>", "network timeout", timeout, 10_000);

  program
    .command("trending")
    .description("list current momentum rankings")
    .option("-w, --window <window>", "6h, 24h, or 7d", "24h")
    .option("-l, --language <language>")
    .option("--topic <topic>")
    .option("-c, --collection <collection>")
    .option("--limit <count>", "number of results", "20")
    .action(async (commandOptions) => {
      const { options, client } = context(program);
      const data = await client.trending({
        window: commandOptions.window,
        language: commandOptions.language,
        topic: commandOptions.topic,
        collection: commandOptions.collection,
        limit: commandOptions.limit,
      });
      output(data, options.json, () => formatTrending(data, options.color));
    });

  program
    .command("inspect <repository>")
    .description("inspect a tracked repository")
    .action(async (input) => {
      const { options, client } = context(program);
      const repository = normalizeRepository(input);
      const data = await client.inspect(repository);
      output(data, options.json, () => formatDetail(data, options.color));
    });

  program
    .command("track <repository>")
    .description("queue a public repository for tracking")
    .action(async (input) => {
      const { options, client } = context(program);
      const repository = normalizeRepository(input);
      const data = await client.track(repository);
      output(data, options.json, () => formatTrack(data, options.color));
    });

  program
    .command("compare <repositories...>")
    .description("compare two to four repositories")
    .option("-w, --window <window>", "6h, 24h, or 7d", "24h")
    .action(async (inputs: string[], commandOptions) => {
      if (inputs.length < 2 || inputs.length > 4)
        throw new ClientError("Compare requires two to four repositories.");
      const { options, client } = context(program);
      const repositories = inputs.map(normalizeRepository);
      const data = await client.compare(repositories, commandOptions.window);
      output(data, options.json, () => formatComparison(data, options.color));
    });

  program
    .command("config")
    .description("show active CLI configuration")
    .action(() => {
      const { options, apiURL } = context(program);
      const data = {
        apiUrl: apiURL,
        version,
        color: options.color,
        timeout: options.timeout,
      };
      output(
        data,
        options.json,
        () =>
          `API URL  ${data.apiUrl}\nVersion  ${data.version}\nColor    ${data.color ? "on" : "off"}\nTimeout  ${data.timeout}ms`,
      );
    });
  return program;
}

export async function main(argv = process.argv) {
  try {
    await buildProgram().parseAsync(argv);
    return 0;
  } catch (error) {
    if (error instanceof ClientError) {
      process.stderr.write(`repometeor: ${error.message}\n`);
      return error.exitCode;
    }
    throw error;
  }
}

if (
  process.argv[1] &&
  import.meta.url === new URL(process.argv[1], "file:").href
) {
  process.exitCode = await main();
}
