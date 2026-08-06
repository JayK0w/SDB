package domain

// Sonde de cible : éprouver une configuration de dépôt AVANT de la créer.
//
// La création initialise déjà le dépôt et annule la ligne si l'init échoue —
// mais `restic init` ne teste que LISTER et ÉCRIRE. Une clé sans droit de
// suppression, ou restreinte au point de ne pas lister les buckets, passe
// l'init et ne casse qu'au premier retrait de verrou ou à la première purge :
// des jours plus tard, sur le dépôt censé rester quand le principal a lâché.
// La sonde exerce les quatre droits dans l'ordre où ils cassent, et nomme
// celui qui manque.

// Étapes de sonde, dans l'ordre d'exécution. Chacune présuppose la précédente :
// inutile de tenter d'écrire des données si l'init a échoué.
const (
	// ProbePair : la paire copie/source ne se dispute pas une variable
	// d'identifiants. Purement local, aucun accès réseau — donc en premier.
	ProbePair = "copy-pair"
	// ProbeInit : lister le backend et y écrire (restic init).
	ProbeInit = "init"
	// ProbeWrite : écrire des DONNÉES et poser un verrou (restic backup).
	ProbeWrite = "write"
	// ProbeRead : relire ce qui vient d'être écrit (restic snapshots).
	ProbeRead = "read"
	// ProbeDelete : SUPPRIMER paquets, index et snapshot (forget --prune).
	// Le seul droit que la création ne teste jamais.
	ProbeDelete = "delete"
)

// ProbeStep : résultat d'une étape. Error est vide quand OK.
type ProbeStep struct {
	Name  string
	OK    bool
	Error string
}

// TargetProbe : compte rendu d'un test de cible.
type TargetProbe struct {
	Steps []ProbeStep
	// Residue : chemin du dépôt de sonde, laissé DERRIÈRE dans la cible.
	//
	// restic ne sait pas détruire un dépôt : `forget --prune` retire les
	// paquets, les index et le snapshot, mais `config` et `keys/` restent —
	// deux objets, quelques centaines d'octets. Le chemin est rendu à
	// l'exploitant pour qu'il puisse les effacer lui-même s'il y tient ;
	// le taire ferait de SDB un outil qui salit sans le dire.
	//
	// Vide tant qu'aucun dépôt de sonde n'a été créé (échec avant l'init).
	Residue string
}

// OK : toutes les étapes exécutées ont réussi.
func (p *TargetProbe) OK() bool {
	if p == nil || len(p.Steps) == 0 {
		return false
	}
	for _, s := range p.Steps {
		if !s.OK {
			return false
		}
	}
	return true
}

// FailedStep : nom de la première étape en échec, vide si tout a réussi.
// C'est ce que l'exploitant doit corriger — les étapes suivantes n'ont pas
// été tentées, leur silence ne dit rien.
func (p *TargetProbe) FailedStep() string {
	if p == nil {
		return ""
	}
	for _, s := range p.Steps {
		if !s.OK {
			return s.Name
		}
	}
	return ""
}

// Pass / Fail : construction des étapes, pour que le moteur ne manipule pas
// la cohérence OK/Error à la main.
func (p *TargetProbe) Pass(name string) {
	p.Steps = append(p.Steps, ProbeStep{Name: name, OK: true})
}

func (p *TargetProbe) Fail(name string, err error) {
	p.Steps = append(p.Steps, ProbeStep{Name: name, OK: false, Error: err.Error()})
}
