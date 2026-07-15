// Générateur de la présentation de soutenance SDB (pptxgenjs)
const pptxgen = require("pptxgenjs");

const pres = new pptxgen();
pres.layout = "LAYOUT_WIDE"; // 13.33 x 7.5

// palette assortie au produit (dark-mode-first)
const C = {
  bg: "0B0B11",
  card: "16161F",
  cardLine: "2B2B38",
  tint: "1B1B2C",
  ink: "F4F4F5",
  mut: "A1A1AA",
  ind: "6366F1",
  indL: "A5B4FC",
  em: "34D399",
  emD: "064E3B",
  amb: "FBBF24",
  red: "F87171",
  redD: "450A0A",
};
const W = 13.33, H = 7.5;
const HEAD = "Arial";
const BODY = "Calibri";

let pageNo = 0;
function base(kicker, title) {
  const s = pres.addSlide();
  s.background = { color: C.bg };
  pageNo++;
  if (kicker) {
    s.addText(kicker.toUpperCase(), {
      x: 0.6, y: 0.32, w: 9.5, h: 0.3, margin: 0,
      fontFace: HEAD, fontSize: 11, bold: true, color: C.indL, charSpacing: 3,
    });
  }
  if (title) {
    s.addText(title, {
      x: 0.6, y: 0.58, w: 12.1, h: 0.62, margin: 0,
      fontFace: HEAD, fontSize: 29, bold: true, color: C.ink,
    });
  }
  s.addText(String(pageNo), {
    x: 12.55, y: 7.05, w: 0.5, h: 0.3, margin: 0, align: "right",
    fontFace: BODY, fontSize: 10, color: "52525B",
  });
  return s;
}

function card(s, x, y, w, h, fill) {
  s.addShape(pres.ShapeType.roundRect, {
    x, y, w, h, rectRadius: 0.09,
    fill: { color: fill || C.card },
    line: { color: C.cardLine, width: 1 },
  });
}

function iconCircle(s, x, y, emoji, color, d) {
  const dia = d || 0.52;
  s.addShape(pres.ShapeType.ellipse, {
    x, y, w: dia, h: dia,
    fill: { color: color, transparency: 82 },
    line: { color: color, width: 1.25 },
  });
  s.addText(emoji, {
    x: x - 0.09, y: y - 0.02, w: dia + 0.18, h: dia + 0.04, margin: 0,
    align: "center", valign: "middle", fontSize: dia >= 0.6 ? 22 : 17, color: C.ink,
  });
}

function arrow(s, x1, y1, x2, y2, color) {
  s.addShape(pres.ShapeType.line, {
    x: Math.min(x1, x2), y: Math.min(y1, y2),
    w: Math.abs(x2 - x1), h: Math.abs(y2 - y1),
    flipH: x2 < x1, flipV: y2 < y1,
    line: { color: color || C.indL, width: 2.25, endArrowType: "triangle" },
  });
}

// ---------------------------------------------------------------- S1 titre
{
  const s = pres.addSlide();
  s.background = { color: C.bg };
  // anneaux décoratifs façon "North Star"
  s.addShape(pres.ShapeType.ellipse, { x: 9.1, y: 0.9, w: 5.4, h: 5.4, fill: { color: C.bg }, line: { color: "26263A", width: 1.5 } });
  s.addShape(pres.ShapeType.ellipse, { x: 10.1, y: 1.9, w: 3.4, h: 3.4, fill: { color: C.bg }, line: { color: "323250", width: 1.5 } });
  s.addShape(pres.ShapeType.ellipse, { x: 11.62, y: 3.42, w: 0.36, h: 0.36, fill: { color: C.em }, line: { type: "none" } });

  s.addText("SOUTENANCE DE PROJET", { x: 0.8, y: 1.55, w: 8, h: 0.35, margin: 0, fontFace: HEAD, fontSize: 13, bold: true, color: C.indL, charSpacing: 4 });
  s.addText("SDB", { x: 0.72, y: 1.95, w: 8, h: 1.55, margin: 0, fontFace: HEAD, fontSize: 92, bold: true, color: C.ind });
  s.addText("Standalone Docker Backup", { x: 0.8, y: 3.55, w: 8.4, h: 0.55, margin: 0, fontFace: HEAD, fontSize: 27, bold: true, color: C.ink });
  s.addText("Sauvegarder et restaurer les volumes Docker,\nchiffrés de bout en bout, en un clic.", { x: 0.8, y: 4.25, w: 7.6, h: 0.9, margin: 0, fontFace: BODY, fontSize: 16, color: C.mut, lineSpacingMultiple: 1.15 });
  s.addText("Go · Vue 3 · restic · Docker      —      2026", { x: 0.8, y: 6.55, w: 8, h: 0.35, margin: 0, fontFace: BODY, fontSize: 12, color: "71717A" });
  s.addNotes("Pitch 30 s : Docker ne sauvegarde pas les volumes de ses conteneurs. SDB est un outil autonome — un seul binaire — qui les snapshotte chiffrés vers 7 types de destinations, et les restaure en un clic depuis une interface web. Tout est développé en Go et Vue 3, l'outil tourne lui-même dans un conteneur durci.");
}

// ---------------------------------------------------------------- S2 problème
{
  const s = base("Contexte", "Le problème : des données critiques sans filet");
  const rows = [
    ["💾", C.ind, "Les données vivent dans les volumes", "bases de données, uploads, configurations : tout l'état persistant d'une application conteneurisée"],
    ["🔥", C.red, "Un incident suffit", "mauvais déploiement, erreur humaine, ransomware — le volume est écrasé ou perdu"],
    ["🧰", C.amb, "Les solutions existantes rebutent", "scripts cron artisanaux, outils en ligne de commande, aucune vision d'ensemble ni restauration simple"],
  ];
  rows.forEach((r, i) => {
    const y = 1.75 + i * 1.42;
    iconCircle(s, 0.7, y, r[0], r[1]);
    s.addText(r[2], { x: 1.5, y: y - 0.06, w: 6.4, h: 0.4, margin: 0, fontFace: HEAD, fontSize: 16.5, bold: true, color: C.ink });
    s.addText(r[3], { x: 1.5, y: y + 0.34, w: 6.4, h: 0.75, margin: 0, fontFace: BODY, fontSize: 13.5, color: C.mut, lineSpacingMultiple: 1.1 });
  });
  card(s, 8.45, 1.75, 4.25, 4.35, C.redD);
  s.addText("⚠", { x: 8.45, y: 2.15, w: 4.25, h: 0.7, margin: 0, align: "center", fontSize: 34, color: C.red });
  s.addText("1 volume perdu", { x: 8.6, y: 3.0, w: 3.95, h: 0.5, margin: 0, align: "center", fontFace: HEAD, fontSize: 24, bold: true, color: C.ink });
  s.addText("= service à l'arrêt", { x: 8.6, y: 3.5, w: 3.95, h: 0.45, margin: 0, align: "center", fontFace: HEAD, fontSize: 19, bold: true, color: C.red });
  s.addText("Docker ne fournit aucun mécanisme natif de sauvegarde des volumes.", { x: 8.75, y: 4.35, w: 3.65, h: 1.2, margin: 0, align: "center", fontFace: BODY, fontSize: 13, italic: true, color: "FCA5A5", lineSpacingMultiple: 1.15 });
  s.addNotes("Poser le décor : en production conteneurisée, tout l'état est dans les volumes. Docker lui-même n'offre rien pour les sauvegarder. Les équipes bricolent des scripts cron — pas d'historique, pas de chiffrement, restauration hasardeuse.");
}

// ---------------------------------------------------------------- S3 solution
{
  const s = base("Notre réponse", "SDB : la sauvegarde des volumes, clé en main");
  const cards = [
    ["📸", C.ind, "Snapshots chiffrés", "instantanés incrémentaux et dédupliqués des volumes (restic), chiffrement AES-256 de bout en bout"],
    ["♻️", C.em, "Restauration en un clic", "retour à n'importe quel snapshot ; le conteneur est arrêté puis relancé automatiquement"],
    ["⏰", C.amb, "Planification & rétention", "sauvegardes récurrentes (cron intégré) et purge automatique selon une politique keep-last / daily / weekly"],
    ["📊", C.indL, "Suivi temps réel", "progression en direct par WebSocket, historique complet, métriques Prometheus"],
  ];
  cards.forEach((cd, i) => {
    const x = 0.6 + (i % 2) * 6.2, y = 1.6 + Math.floor(i / 2) * 2.35;
    card(s, x, y, 5.95, 2.1);
    iconCircle(s, x + 0.3, y + 0.3, cd[0], cd[1]);
    s.addText(cd[2], { x: x + 1.05, y: y + 0.28, w: 4.7, h: 0.45, margin: 0, fontFace: HEAD, fontSize: 17, bold: true, color: C.ink });
    s.addText(cd[3], { x: x + 0.35, y: y + 0.95, w: 5.3, h: 1.0, margin: 0, fontFace: BODY, fontSize: 13.5, color: C.mut, lineSpacingMultiple: 1.12 });
  });
  s.addText([
    { text: "Un seul binaire", options: { bold: true, color: C.em } },
    { text: "   ·   interface web embarquée   ·   ", options: { color: C.mut } },
    { text: "7 destinations de stockage", options: { bold: true, color: C.em } },
  ], { x: 0.6, y: 6.45, w: 12.1, h: 0.45, margin: 0, align: "center", fontFace: BODY, fontSize: 16 });
  s.addNotes("Les 4 piliers fonctionnels. Insister : tout est intégré dans un binaire unique — pas de dépendances à installer, l'interface web est embarquée dedans.");
}

// ---------------------------------------------------------------- S4 stack
{
  const s = base("Technologies", "Une stack moderne, sobre et typée");
  const cols = [
    ["🧠", C.ind, "Backend", ["Go 1.25", "Gin — API REST", "SQLite (pur Go, sans CGO)", "SDK Docker officiel", "restic 0.18 (moteur de snapshots)"]],
    ["🎨", C.em, "Frontend", ["Vue 3 (Composition API)", "TypeScript strict", "Tailwind CSS 4", "Pinia (état global)", "WebSocket natif"]],
    ["🚢", C.amb, "Livraison", ["Docker multi-stage", "image distroless minimale", "compose durci prêt à l'emploi", "CI GitHub Actions", "migrations SQL versionnées"]],
  ];
  cols.forEach((col, i) => {
    const x = 0.6 + i * 4.22;
    card(s, x, 1.6, 3.95, 5.1);
    iconCircle(s, x + 0.35, 1.95, col[0], col[1], 0.62);
    s.addText(col[2], { x: x + 1.15, y: 2.02, w: 2.6, h: 0.5, margin: 0, fontFace: HEAD, fontSize: 19, bold: true, color: C.ink });
    s.addText(col[3].map((t, j) => ({ text: t, options: { bullet: { code: "2022", indent: 14 }, breakLine: j < col[3].length - 1 } })), { x: x + 0.4, y: 3.0, w: 3.25, h: 3.4, margin: 0, fontFace: BODY, fontSize: 14.5, color: "D4D4D8", paraSpaceAfter: 12 });
  });
  s.addNotes("Choix assumés : SQLite pur Go pour un binaire statique sans CGO ; restic plutôt que réinventer un moteur de sauvegarde (chiffrement, déduplication et incrémental éprouvés) ; TypeScript strict vérifié en CI.");
}

// ---------------------------------------------------------------- S5 architecture
{
  const s = base("Architecture logicielle", "Clean Architecture : le métier au centre");
  const pts = [
    ["Les dépendances pointent vers le cœur", "les couches externes dépendent du domaine, jamais l'inverse"],
    ["Le métier ignore la technique", "aucun import de Docker, SQLite ou Gin dans le domaine et les usecases"],
    ["Chaque adaptateur est remplaçable", "et testable isolément : 63 tests automatisés sur des fakes"],
  ];
  pts.forEach((p, i) => {
    const y = 1.9 + i * 1.5;
    s.addShape(pres.ShapeType.ellipse, { x: 0.65, y: y + 0.05, w: 0.16, h: 0.16, fill: { color: C.em }, line: { type: "none" } });
    s.addText(p[0], { x: 1.0, y: y - 0.08, w: 4.1, h: 0.65, margin: 0, fontFace: HEAD, fontSize: 14.5, bold: true, color: C.ink, lineSpacingMultiple: 1.05 });
    s.addText(p[1], { x: 1.0, y: y + 0.52, w: 4.1, h: 0.8, margin: 0, fontFace: BODY, fontSize: 12.5, color: C.mut, lineSpacingMultiple: 1.1 });
  });

  // diagramme en couches
  const dx = 5.7, dw = 7.0;
  card(s, dx, 1.55, 3.4, 1.05, C.tint);
  s.addText("API HTTP + WebSocket\n(Gin, JWT, hub)", { x: dx, y: 1.55, w: 3.4, h: 1.05, margin: 0, align: "center", valign: "middle", fontFace: HEAD, fontSize: 12.5, bold: true, color: C.indL, lineSpacingMultiple: 1.05 });
  card(s, dx + 3.6, 1.55, 3.4, 1.05, C.tint);
  s.addText("Infrastructure\n(SQLite · Docker · restic · crypto)", { x: dx + 3.6, y: 1.55, w: 3.4, h: 1.05, margin: 0, align: "center", valign: "middle", fontFace: HEAD, fontSize: 12.5, bold: true, color: C.indL, lineSpacingMultiple: 1.05 });
  arrow(s, dx + 1.7, 2.68, dx + 2.6, 3.32);
  arrow(s, dx + 5.3, 2.68, dx + 4.4, 3.32);
  card(s, dx + 0.9, 3.4, 5.2, 1.0, "232338");
  s.addText("Usecases — orchestration métier\nbackup · restore · scheduler · auth · maintenance", { x: dx + 0.9, y: 3.4, w: 5.2, h: 1.0, margin: 0, align: "center", valign: "middle", fontFace: HEAD, fontSize: 12.5, bold: true, color: C.ink, lineSpacingMultiple: 1.05 });
  arrow(s, dx + 3.5, 4.48, dx + 3.5, 5.05);
  card(s, dx + 1.55, 5.12, 3.9, 1.0, C.emD);
  s.addText("Domain\nentités + interfaces (ports)", { x: dx + 1.55, y: 5.12, w: 3.9, h: 1.0, margin: 0, align: "center", valign: "middle", fontFace: HEAD, fontSize: 13, bold: true, color: C.em, lineSpacingMultiple: 1.05 });
  s.addText("cmd/sdb/main.go assemble les couches au démarrage (injection de dépendances)", { x: dx, y: 6.45, w: dw, h: 0.4, margin: 0, align: "center", fontFace: BODY, fontSize: 11.5, italic: true, color: "71717A" });
  s.addNotes("Slide clé pour le jury technique. La règle unique : les flèches de dépendance pointent vers le domaine. Concrètement : le domaine définit des interfaces (ports) ; SQLite, Docker et restic ne sont que des implémentations interchangeables. C'est ce qui permet 63 tests rapides sans démon Docker.");
}

// ---------------------------------------------------------------- S6 worker
{
  const s = base("Le cœur du système", "Le worker éphémère : SDB ne touche jamais aux données");
  const y0 = 2.6, bh = 1.35;
  // SDB au-dessus
  card(s, 5.14, 1.35, 3.05, 0.85, "232338");
  s.addText("SDB  (orchestrateur)", { x: 5.14, y: 1.35, w: 3.05, h: 0.85, margin: 0, align: "center", valign: "middle", fontFace: HEAD, fontSize: 14, bold: true, color: C.indL });
  arrow(s, 6.0, 2.24, 3.4, y0 - 0.04, "52525B");
  arrow(s, 7.3, 2.24, 8.2, y0 - 0.04, "52525B");
  // 3 boîtes du flux
  card(s, 0.7, y0, 3.6, bh);
  s.addText("🐳  Conteneur cible\n+ volume de données", { x: 0.7, y: y0, w: 3.6, h: bh, margin: 0, align: "center", valign: "middle", fontFace: HEAD, fontSize: 13.5, bold: true, color: C.ink, lineSpacingMultiple: 1.1 });
  card(s, 5.0, y0, 3.3, bh, C.emD);
  s.addText("⚙️  Worker restic\néphémère", { x: 5.0, y: y0, w: 3.3, h: bh, margin: 0, align: "center", valign: "middle", fontFace: HEAD, fontSize: 13.5, bold: true, color: C.em, lineSpacingMultiple: 1.1 });
  card(s, 9.05, y0, 3.6, bh);
  s.addText("☁️  Dépôt chiffré\nlocal ou cloud", { x: 9.05, y: y0, w: 3.6, h: bh, margin: 0, align: "center", valign: "middle", fontFace: HEAD, fontSize: 13.5, bold: true, color: C.ink, lineSpacingMultiple: 1.1 });
  arrow(s, 4.32, y0 + bh / 2, 4.98, y0 + bh / 2, C.em);
  arrow(s, 8.32, y0 + bh / 2, 9.03, y0 + bh / 2, C.em);
  s.addText("volume monté en LECTURE SEULE", { x: 3.05, y: y0 + bh + 0.12, w: 3.6, h: 0.3, margin: 0, align: "center", fontFace: BODY, fontSize: 11, bold: true, color: C.em });
  s.addText("flux chiffré AES-256", { x: 7.35, y: y0 + bh + 0.12, w: 2.9, h: 0.3, margin: 0, align: "center", fontFace: BODY, fontSize: 11, bold: true, color: C.em });

  const pts = [
    "SDB orchestre via l'API Docker : il ne lit jamais le contenu des volumes lui-même",
    "le worker est créé pour une opération puis systématiquement détruit — aucun résidu",
    "dépôt local : le worker tourne sans aucun accès réseau (network none)",
  ];
  s.addText(pts.map((t, j) => ({ text: t, options: { bullet: { code: "2022", indent: 14 }, breakLine: j < pts.length - 1 } })), { x: 1.3, y: 5.05, w: 10.8, h: 1.7, margin: 0, fontFace: BODY, fontSize: 14.5, color: "D4D4D8", paraSpaceAfter: 12 });
  s.addNotes("Le choix d'architecture le plus important côté sécurité : SDB délègue tout le travail sur les données à un conteneur jetable. Les secrets du dépôt (mot de passe restic, clés cloud) sont injectés en mémoire dans ce worker et disparaissent avec lui.");
}

// ---------------------------------------------------------------- S7 cycle
{
  const s = base("Fonctionnement", "Le cycle d'une sauvegarde, étape par étape");
  s.addText([
    { text: "L'API répond 202 immédiatement", options: { bold: true, color: C.indL } },
    { text: " — le pipeline tourne en arrière-plan, la progression arrive en WebSocket.", options: { color: C.mut } },
  ], { x: 0.6, y: 1.32, w: 12.1, h: 0.4, margin: 0, fontFace: BODY, fontSize: 14 });
  const steps = [
    ["1", C.ind, "Pre-hook", "commande exécutée dans le conteneur (ex. pg_dumpall)"],
    ["2", C.ind, "Arrêt optionnel", "sauvegarde « à froid » pour une cohérence parfaite"],
    ["3", C.ind, "Snapshot", "worker restic, volumes montés en lecture seule"],
    ["4", C.em, "Redémarrage GARANTI", "le conteneur repart même en cas d'échec ou d'annulation"],
    ["5", C.ind, "Post-hook", "nettoyage (suppression du dump temporaire…)"],
    ["6", C.ind, "Rétention", "restic forget --prune selon la politique définie"],
  ];
  steps.forEach((st, i) => {
    const x = 0.6 + (i % 3) * 4.22, y = 2.0 + Math.floor(i / 3) * 2.35;
    card(s, x, y, 3.95, 2.1, st[1] === C.em ? C.emD : C.card);
    s.addShape(pres.ShapeType.ellipse, { x: x + 0.3, y: y + 0.3, w: 0.55, h: 0.55, fill: { color: st[1], transparency: 78 }, line: { color: st[1], width: 1.25 } });
    s.addText(st[0], { x: x + 0.3, y: y + 0.3, w: 0.55, h: 0.55, margin: 0, align: "center", valign: "middle", fontFace: HEAD, fontSize: 18, bold: true, color: st[1] === C.em ? C.em : C.indL });
    s.addText(st[2], { x: x + 1.05, y: y + 0.33, w: 2.8, h: 0.55, margin: 0, fontFace: HEAD, fontSize: 15, bold: true, color: st[1] === C.em ? C.em : C.ink, valign: "middle" });
    s.addText(st[3], { x: x + 0.35, y: y + 1.05, w: 3.3, h: 0.95, margin: 0, fontFace: BODY, fontSize: 12.5, color: st[1] === C.em ? "A7F3D0" : C.mut, lineSpacingMultiple: 1.1 });
  });
  s.addNotes("Dérouler les 6 étapes. Point différenciant à marteler : l'étape 4. Le redémarrage tourne sur un contexte insensible à l'annulation — quoi qu'il arrive (échec restic, crash, annulation utilisateur), le conteneur de production repart. Un hook pré qui échoue annule la sauvegarde par défaut : mieux vaut pas de snapshot qu'un snapshot incohérent.");
}

// ---------------------------------------------------------------- S8 données
{
  const s = base("Persistance", "Où sont stockées les données ?");
  const rows = [
    ["🗄️", C.ind, "Métadonnées  →  SQLite (volume Docker /data)", "comptes utilisateurs, historiques de sauvegarde et de restauration, planifications — les secrets y sont chiffrés AES-256-GCM sous la clé maître"],
    ["📦", C.em, "Sauvegardes  →  dépôts restic", "chiffrées de bout en bout, dédupliquées, incrémentales — sur disque local, S3, Backblaze B2, Azure, Google Cloud, SFTP ou serveur REST"],
    ["🔑", C.amb, "Secrets d'exploitation  →  fichiers (Docker secrets)", "clé maître et secret JWT montés en fichiers dans le conteneur — jamais en clair dans le code, la base ou les variables d'environnement du dépôt Git"],
  ];
  rows.forEach((r, i) => {
    const y = 1.65 + i * 1.62;
    card(s, 0.6, y, 12.1, 1.42);
    iconCircle(s, 0.95, y + 0.42, r[0], r[1]);
    s.addText(r[2], { x: 1.75, y: y + 0.18, w: 10.6, h: 0.45, margin: 0, fontFace: HEAD, fontSize: 15.5, bold: true, color: C.ink });
    s.addText(r[3], { x: 1.75, y: y + 0.65, w: 10.6, h: 0.7, margin: 0, fontFace: BODY, fontSize: 13, color: C.mut, lineSpacingMultiple: 1.1 });
  });
  s.addText([
    { text: "⚠  La clé maître est vitale : ", options: { bold: true, color: C.amb } },
    { text: "sans elle, les configurations de stockage sont indéchiffrables — elle est sauvegardée séparément des données.", options: { color: C.mut } },
  ], { x: 0.6, y: 6.6, w: 12.1, h: 0.5, margin: 0, fontFace: BODY, fontSize: 13.5 });
  s.addNotes("Trois niveaux : les métadonnées (SQLite dans un volume dédié), les sauvegardes elles-mêmes (dépôts restic, chiffrement bout en bout — même un cloud compromis ne révèle rien), et les secrets d'exploitation en Docker secrets. Anticiper la question du jury : « et si on perd la clé maître ? » → les données restic restent restaurables avec le mot de passe du dépôt ; ce sont les configs SDB qu'il faudrait recréer.");
}

// ---------------------------------------------------------------- S9 destinations
{
  const s = base("Interopérabilité", "7 destinations de stockage");
  const chips = [
    ["💽", "Disque local", "réseau coupé"],
    ["🪣", "S3 compatible", "AWS, MinIO, Scaleway…"],
    ["🔵", "Backblaze B2", "cloud économique"],
    ["🔷", "Azure Blob", "Microsoft Cloud"],
    ["🟡", "Google Cloud", "compte de service"],
    ["🔐", "SFTP", "autre serveur via SSH"],
    ["🌐", "Serveur REST", "restic rest-server"],
  ];
  chips.forEach((c, i) => {
    const x = 0.6 + (i % 4) * 3.22, y = 1.75 + Math.floor(i / 4) * 2.15;
    card(s, x, y, 2.95, 1.9);
    s.addText(c[0], { x: x, y: y + 0.22, w: 2.95, h: 0.55, margin: 0, align: "center", fontSize: 26 });
    s.addText(c[1], { x: x + 0.1, y: y + 0.85, w: 2.75, h: 0.4, margin: 0, align: "center", fontFace: HEAD, fontSize: 14.5, bold: true, color: C.ink });
    s.addText(c[2], { x: x + 0.1, y: y + 1.28, w: 2.75, h: 0.4, margin: 0, align: "center", fontFace: BODY, fontSize: 11.5, color: C.mut });
  });
  card(s, 10.26, 3.9, 2.44, 1.9, C.tint);
  s.addText("+", { x: 10.26, y: 4.05, w: 2.44, h: 0.6, margin: 0, align: "center", fontFace: HEAD, fontSize: 28, bold: true, color: C.indL });
  s.addText("extensible :\nun backend =\n~20 lignes", { x: 10.36, y: 4.75, w: 2.24, h: 0.95, margin: 0, align: "center", fontFace: BODY, fontSize: 11.5, color: C.indL, lineSpacingMultiple: 1.1 });
  s.addText([
    { text: "Clés SSH et comptes de service ", options: { color: C.mut } },
    { text: "injectés dans le worker en fichiers 0600", options: { bold: true, color: C.em } },
    { text: " — jamais écrits sur le disque de l'hôte.", options: { color: C.mut } },
  ], { x: 0.6, y: 6.35, w: 12.1, h: 0.45, margin: 0, align: "center", fontFace: BODY, fontSize: 13.5 });
  s.addNotes("Même mot de passe de dépôt et même flux quel que soit le backend : seuls changent l'URL et les identifiants. Pour le SFTP et Google Cloud, la clé privée / le JSON de compte de service sont copiés dans le worker par l'API Docker (tar en mémoire, permissions 0600) puis détruits avec lui.");
}

// ---------------------------------------------------------------- S10 sécurité
{
  const s = base("Sécurité", "Défense en profondeur, à chaque couche");
  const items = [
    ["🔐", "Argon2id", "hachage des mots de passe utilisateurs (recommandations OWASP)"],
    ["🧊", "AES-256-GCM", "identifiants cloud et mots de passe restic chiffrés au repos"],
    ["🎫", "JWT HS256 épinglé", "anti-confusion d'algorithme + rate-limit sur le login"],
    ["📦", "Conteneur durci", "rootfs read-only · cap_drop ALL · no-new-privileges"],
    ["🏠", "Loopback only", "port publié sur 127.0.0.1 : le contournement d'UFW par Docker est neutralisé"],
    ["🤝", "mTLS obligatoire", "connexion tcp:// au démon Docker refusée sans certificats"],
  ];
  items.forEach((it, i) => {
    const x = 0.6 + (i % 3) * 4.22, y = 1.7 + Math.floor(i / 3) * 2.5;
    card(s, x, y, 3.95, 2.25);
    iconCircle(s, x + 0.32, y + 0.3, it[0], C.ind);
    s.addText(it[1], { x: x + 1.02, y: y + 0.32, w: 2.85, h: 0.5, margin: 0, fontFace: HEAD, fontSize: 15, bold: true, color: C.ink, valign: "middle" });
    s.addText(it[2], { x: x + 0.35, y: y + 1.0, w: 3.3, h: 1.1, margin: 0, fontFace: BODY, fontSize: 12.5, color: C.mut, lineSpacingMultiple: 1.12 });
  });
  s.addNotes("La sécurité était la priorité n°1 du cahier des charges. Détail qui marque : le port loopback. Beaucoup ignorent que publier un port Docker contourne le pare-feu UFW — SDB force 127.0.0.1 et documente le reverse proxy TLS pour l'accès distant. Le conteneur n'a AUCUNE capability Linux.");
}

// ---------------------------------------------------------------- S11 temps réel
{
  const s = base("Observabilité", "Temps réel et supervision");
  card(s, 0.6, 1.6, 5.95, 5.1);
  iconCircle(s, 0.95, 1.95, "⚡", C.ind, 0.6);
  s.addText("Hub WebSocket", { x: 1.75, y: 2.02, w: 4.3, h: 0.5, margin: 0, fontFace: HEAD, fontSize: 18, bold: true, color: C.ink });
  // mini flux
  card(s, 1.0, 2.95, 1.5, 0.75, C.tint);
  s.addText("jobs", { x: 1.0, y: 2.95, w: 1.5, h: 0.75, margin: 0, align: "center", valign: "middle", fontFace: BODY, fontSize: 12.5, bold: true, color: C.indL });
  arrow(s, 2.55, 3.32, 3.1, 3.32);
  card(s, 3.12, 2.95, 1.35, 0.75, C.tint);
  s.addText("hub", { x: 3.12, y: 2.95, w: 1.35, h: 0.75, margin: 0, align: "center", valign: "middle", fontFace: BODY, fontSize: 12.5, bold: true, color: C.indL });
  arrow(s, 4.52, 3.32, 5.05, 3.32);
  card(s, 5.07, 2.95, 1.15, 0.75, C.tint);
  s.addText("UI", { x: 5.07, y: 2.95, w: 1.15, h: 0.75, margin: 0, align: "center", valign: "middle", fontFace: BODY, fontSize: 12.5, bold: true, color: C.indL });
  const wsPts = [
    "progression des sauvegardes et restaurations en direct",
    "diffusion non bloquante : un navigateur lent est déconnecté, jamais attendu",
    "reconnexion automatique côté client (backoff exponentiel)",
  ];
  s.addText(wsPts.map((t, j) => ({ text: t, options: { bullet: { code: "2022", indent: 14 }, breakLine: j < wsPts.length - 1 } })), { x: 1.0, y: 4.1, w: 5.2, h: 2.3, margin: 0, fontFace: BODY, fontSize: 13, color: "D4D4D8", paraSpaceAfter: 10 });

  card(s, 6.85, 1.6, 5.85, 5.1);
  iconCircle(s, 7.2, 1.95, "📈", C.em, 0.6);
  s.addText("Métriques Prometheus", { x: 8.0, y: 2.02, w: 4.4, h: 0.5, margin: 0, fontFace: HEAD, fontSize: 18, bold: true, color: C.ink });
  card(s, 7.2, 2.95, 5.1, 1.85, "0F172A");
  s.addText("sdb_backups_total{status}\nsdb_restores_total{status}\nsdb_running_jobs\nsdb_last_backup_success_\n    timestamp_seconds{container}", { x: 7.45, y: 3.1, w: 4.7, h: 1.6, margin: 0, fontFace: "Courier New", fontSize: 12, color: C.em, lineSpacingMultiple: 1.18 });
  s.addText([
    { text: "Alerte type : ", options: { bold: true, color: C.ink } },
    { text: "« aucun snapshot réussi depuis 24 h pour ce conteneur » — l'endpoint /metrics est protégé par un jeton dédié et désactivé par défaut.", options: { color: C.mut } },
  ], { x: 7.2, y: 5.05, w: 5.15, h: 1.4, margin: 0, fontFace: BODY, fontSize: 13, lineSpacingMultiple: 1.15 });
  s.addNotes("Deux canaux complémentaires : le WebSocket pour l'humain devant le dashboard, Prometheus pour la supervision automatisée. Les deux sont alimentés par le même flux d'événements interne — le collecteur de métriques n'a nécessité aucun changement dans la logique métier.");
}

// ---------------------------------------------------------------- S12 qualité
{
  const s = base("Ingénierie", "Qualité et industrialisation");
  const stats = [
    ["≈ 10 800", "lignes de code", C.ind],
    ["63", "tests automatisés", C.em],
    ["31", "endpoints API REST", C.indL],
    ["100 %", "TypeScript strict", C.amb],
  ];
  stats.forEach((st, i) => {
    const x = 0.6 + i * 3.22;
    card(s, x, 1.6, 2.95, 1.9);
    s.addText(st[0], { x: x + 0.1, y: 1.85, w: 2.75, h: 0.85, margin: 0, align: "center", fontFace: HEAD, fontSize: 34, bold: true, color: st[2] });
    s.addText(st[1], { x: x + 0.1, y: 2.75, w: 2.75, h: 0.45, margin: 0, align: "center", fontFace: BODY, fontSize: 13, color: C.mut });
  });
  card(s, 0.6, 3.85, 5.95, 2.85);
  iconCircle(s, 0.95, 4.15, "🔁", C.ind);
  s.addText("Intégration continue", { x: 1.68, y: 4.2, w: 4.5, h: 0.45, margin: 0, fontFace: HEAD, fontSize: 16, bold: true, color: C.ink });
  const ciPts = ["go vet + tests avec détecteur de concurrence", "vue-tsc (type-check strict) + build Vite", "construction de l'image Docker complète"];
  s.addText(ciPts.map((t, j) => ({ text: t, options: { bullet: { code: "2022", indent: 14 }, breakLine: j < ciPts.length - 1 } })), { x: 1.0, y: 4.85, w: 5.2, h: 1.7, margin: 0, fontFace: BODY, fontSize: 13, color: "D4D4D8", paraSpaceAfter: 10 });
  card(s, 6.85, 3.85, 5.85, 2.85);
  iconCircle(s, 7.2, 4.15, "✅", C.em);
  s.addText("Validé en conditions réelles", { x: 7.93, y: 4.2, w: 4.6, h: 0.45, margin: 0, fontFace: HEAD, fontSize: 16, bold: true, color: C.ink });
  const e2ePts = ["planification cron déclenchée à la seconde près", "rétention vérifiée : 15 sauvegardes → 3 snapshots conservés", "cycle complet sauvegarde → perte → restauration à l'identique"];
  s.addText(e2ePts.map((t, j) => ({ text: t, options: { bullet: { code: "2022", indent: 14 }, breakLine: j < e2ePts.length - 1 } })), { x: 7.25, y: 4.85, w: 5.2, h: 1.7, margin: 0, fontFace: BODY, fontSize: 13, color: "D4D4D8", paraSpaceAfter: 10 });
  s.addNotes("Chiffres réels mesurés sur le dépôt Git. Les 63 tests tournent sans Docker grâce à l'architecture en ports : le pipeline complet de sauvegarde est testé avec des fakes (conflit, annulation, rollback, hooks). La validation e2e a été faite contre un vrai démon Docker.");
}

// ---------------------------------------------------------------- S13 démo
{
  const s = base("Démonstration", "La preuve en direct : perdre puis retrouver ses données");
  const steps = [
    ["🟢", C.emD, C.em, "Page v1", "une app nginx sert sa page depuis un volume"],
    ["📸", C.tint, C.indL, "Snapshot", "sauvegarde du volume depuis le dashboard"],
    ["💥", C.redD, C.red, "Incident", "un déploiement écrase les données : page rouge"],
    ["♻️", C.tint, C.indL, "Restauration", "un clic — nginx arrêté, volume réécrit, relancé"],
    ["🟢", C.emD, C.em, "Page v1 !", "les données d'origine sont revenues"],
  ];
  steps.forEach((st, i) => {
    const x = 0.72 + i * 2.49, y = 2.2;
    card(s, x, y, 2.22, 2.9, st[1]);
    s.addText(st[0], { x: x, y: y + 0.3, w: 2.22, h: 0.6, margin: 0, align: "center", fontSize: 29 });
    s.addText(st[3], { x: x + 0.08, y: y + 1.05, w: 2.06, h: 0.45, margin: 0, align: "center", fontFace: HEAD, fontSize: 14.5, bold: true, color: st[2] });
    s.addText(st[4], { x: x + 0.13, y: y + 1.55, w: 1.96, h: 1.2, margin: 0, align: "center", fontFace: BODY, fontSize: 11, color: C.mut, lineSpacingMultiple: 1.12 });
    if (i < steps.length - 1) arrow(s, x + 2.24, y + 1.45, x + 2.47, y + 1.45, "52525B");
  });
  s.addText([
    { text: "Application témoin : ", options: { color: C.mut } },
    { text: "http://127.0.0.1:8081", options: { bold: true, color: C.indL } },
    { text: "      ·      Dashboard SDB : ", options: { color: C.mut } },
    { text: "http://127.0.0.1:8080", options: { bold: true, color: C.indL } },
  ], { x: 0.6, y: 5.75, w: 12.1, h: 0.45, margin: 0, align: "center", fontFace: BODY, fontSize: 14.5 });
  s.addText("Restauration complète en une dizaine de secondes, historisée et visible en temps réel.", { x: 0.6, y: 6.3, w: 12.1, h: 0.4, margin: 0, align: "center", fontFace: BODY, fontSize: 13, italic: true, color: "71717A" });
  s.addNotes("Annoncer la démo AVANT de la faire : « nous allons volontairement détruire les données d'une application et les récupérer devant vous ». Laisser la page rouge visible quelques secondes avant de restaurer — c'est le moment où la valeur devient évidente.");
}

// ---------------------------------------------------------------- S14 close
{
  const s = pres.addSlide();
  s.background = { color: C.bg };
  s.addShape(pres.ShapeType.ellipse, { x: 9.6, y: -3.4, w: 6.4, h: 6.4, fill: { color: C.bg }, line: { color: "26263A", width: 1.5 } });
  s.addText("Merci", { x: 0.8, y: 1.1, w: 11.7, h: 1.1, margin: 0, fontFace: HEAD, fontSize: 54, bold: true, color: C.ink });
  s.addText("Des questions ?", { x: 0.8, y: 2.2, w: 11.7, h: 0.55, margin: 0, fontFace: HEAD, fontSize: 22, color: C.indL });
  s.addText("ET APRÈS ?", { x: 0.8, y: 3.45, w: 6, h: 0.35, margin: 0, fontFace: HEAD, fontSize: 12, bold: true, color: C.mut, charSpacing: 3 });
  const next = [
    "notifications d'échec (e-mail, webhook)",
    "gestion de plusieurs hôtes Docker",
    "TLS natif sur l'interface",
    "publication open source du projet",
  ];
  s.addText(next.map((t, j) => ({ text: t, options: { bullet: { code: "2022", indent: 14 }, breakLine: j < next.length - 1 } })), { x: 0.85, y: 3.9, w: 5.6, h: 2.4, margin: 0, fontFace: BODY, fontSize: 14.5, color: "D4D4D8", paraSpaceAfter: 11 });
  card(s, 7.3, 3.45, 5.4, 2.9, C.tint);
  s.addText("SDB en une phrase", { x: 7.65, y: 3.75, w: 4.7, h: 0.4, margin: 0, fontFace: HEAD, fontSize: 14, bold: true, color: C.indL });
  s.addText("Un binaire unique qui sauvegarde les volumes Docker, chiffrés de bout en bout, vers 7 destinations — et les restaure en un clic.", { x: 7.65, y: 4.3, w: 4.75, h: 1.8, margin: 0, fontFace: BODY, fontSize: 15, color: C.ink, lineSpacingMultiple: 1.25 });
  s.addNotes("Conclure sur la phrase de synthèse, ouvrir aux questions. Réponses probables à préparer : pourquoi restic plutôt qu'un moteur maison (fiabilité éprouvée, chiffrement audité), pourquoi Go (binaire statique, concurrence), limites actuelles (un seul hôte Docker, pas de notifications).");
}

pres.writeFile({ fileName: "SDB-soutenance.pptx" }).then(() => console.log("OK: SDB-soutenance.pptx"));
