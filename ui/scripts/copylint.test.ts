import { expect, test } from "bun:test";
import en from "../src/copy/en.json";
import { lintString, lintTree } from "./copylint";

test("SCN-UX-009 every UI string passes the copy lint and a 21-word string fails", () => {
  expect(lintTree(en)).toEqual([]);
  const long = Array.from({ length: 21 }, (_, i) => `word${i}`).join(" ") + ".";
  const problems = lintString("test.long", long);
  expect(problems.length).toBe(1);
  expect(problems[0].reason).toContain("21 words");
  expect(lintString("test.ok", Array.from({ length: 20 }, () => "word").join(" ") + ".")).toEqual([]);
  expect(lintString("test.banned", "Ask the agent.")[0].reason).toContain("agent");
});
