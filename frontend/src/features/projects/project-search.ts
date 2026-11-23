import type { AppEntry } from "../../models/app";

function normalize(value: string) {
  return value.normalize("NFKC").toLocaleLowerCase();
}

function withinOneEdit(word: string, term: string) {
  if (word.length < term.length) return withinOneEdit(term, word);
  if (word.length - term.length > 1) return false;
  let index = 0;
  while (index < term.length && word[index] === term[index]) index++;
  if (word.length !== term.length)
    return word.slice(index + 1) === term.slice(index);
  return (
    word.slice(index + 1) === term.slice(index + 1) ||
    (word[index] === term[index + 1] &&
      word[index + 1] === term[index] &&
      word.slice(index + 2) === term.slice(index + 2))
  );
}

function looselyIncludes(field: string, term: string) {
  if (field.includes(term)) return true;
  if (term.length < 2) return false;
  return field.split(/[^\p{L}\p{N}]+/u).some((word) => {
    let index = 0;
    for (const character of word) {
      if (character === term[index] && ++index === term.length) return true;
    }
    return term.length >= 4 && withinOneEdit(word, term);
  });
}

export function searchProjects(apps: AppEntry[], query: string) {
  const terms = normalize(query).trim().split(/\s+/);
  if (!terms[0]) return apps;
  return apps.filter((app) => {
    const fields = [
      app.name,
      app.description ?? "",
      app.path,
      ...(app.keywords ?? []),
    ].map(normalize);
    return terms.every((term) =>
      fields.some((field) => looselyIncludes(field, term)),
    );
  });
}
