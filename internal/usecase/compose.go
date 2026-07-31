package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/standalone-docker-backup/sdb/internal/domain"
)

// CloneCompose : rend un docker-compose.yml prêt à lancer un second service
// branché sur le volume cloné, à côté du service d'origine qui continue de
// tourner sur le sien.
//
// SDB ne décrit que ce qu'il connaît avec certitude — l'image et le point de
// montage. Ports, variables d'environnement, réseaux et commande ne sont PAS
// devinés : ils sortent en commentaires à compléter. Recopier l'environnement
// automatiquement exfiltrerait les secrets du conteneur d'origine dans un
// fichier, et publier les mêmes ports empêcherait le clone de démarrer.
func (s *RestoreService) CloneCompose(ctx context.Context, containerRef, sourceVolume, targetVolume string) (string, error) {
	if containerRef == "" {
		return "", fmt.Errorf("%w: container is required", domain.ErrInvalidInput)
	}
	container, err := s.containers.Get(ctx, containerRef)
	if err != nil {
		return "", fmt.Errorf("inspecting container: %w", err)
	}
	return renderCloneCompose(container, sourceVolume, targetVolume)
}

func renderCloneCompose(c *domain.Container, sourceVolume, targetVolume string) (string, error) {
	if !domain.ValidVolumeName(sourceVolume) {
		return "", fmt.Errorf("%w: %q is not a valid docker volume name", domain.ErrInvalidInput, sourceVolume)
	}
	if !domain.ValidVolumeName(targetVolume) {
		return "", fmt.Errorf("%w: %q is not a valid docker volume name", domain.ErrInvalidInput, targetVolume)
	}
	if c.Image == "" {
		return "", fmt.Errorf("%w: container %s has no image to clone", domain.ErrInvalidInput, c.Name)
	}

	var mountPoint string
	for _, m := range c.Mounts {
		if m.Type == domain.MountVolume && m.Name == sourceVolume {
			mountPoint = m.Destination
			break
		}
	}
	if mountPoint == "" {
		return "", fmt.Errorf("%w: container %s has no volume named %s", domain.ErrNotFound, c.Name, sourceVolume)
	}

	service := composeName(c.Name) + "-clone"

	var b strings.Builder
	fmt.Fprintf(&b, "# Genere par SDB — clone du volume %q de %q.\n", sourceVolume, c.Name)
	fmt.Fprintf(&b, "# Le volume %q a deja ete restaure par SDB : il existe et contient les donnees.\n", targetVolume)
	b.WriteString("#\n")
	b.WriteString("# A completer avant de lancer — SDB ne les connait pas et ne les invente pas :\n")
	b.WriteString("#   ports       : publier sur des ports LIBRES (le service d'origine occupe les siens)\n")
	b.WriteString("#   environment : recopier les variables du service d'origine\n")
	b.WriteString("#   networks    : rattacher au reseau voulu si le service en depend\n")
	b.WriteString("#   command     : reprendre la commande d'origine si elle est surchargee\n")
	b.WriteString("#\n")
	b.WriteString("#   docker compose -f docker-compose.clone.yml up -d\n")
	b.WriteString("\n")

	b.WriteString("services:\n")
	fmt.Fprintf(&b, "  %s:\n", service)
	fmt.Fprintf(&b, "    image: %s\n", yamlQuote(c.Image))
	b.WriteString("    restart: unless-stopped\n")
	b.WriteString("    volumes:\n")
	fmt.Fprintf(&b, "      - %s\n", yamlQuote(targetVolume+":"+mountPoint))

	// Les autres montages du conteneur d'origine ne sont PAS repris : les
	// partager ferait ecrire deux services dans les memes donnees.
	for _, m := range c.Mounts {
		if m.Type == domain.MountVolume && m.Name == sourceVolume {
			continue
		}
		switch m.Type {
		case domain.MountVolume:
			fmt.Fprintf(&b, "      # - %s   # partage avec %s : cloner ce volume aussi si le service y ecrit\n",
				yamlQuote(m.Name+":"+m.Destination), c.Name)
		case domain.MountBind:
			fmt.Fprintf(&b, "      # - %s   # bind partage avec %s : ecriture concurrente possible\n",
				yamlQuote(m.Source+":"+m.Destination), c.Name)
		}
	}

	b.WriteString("    # ports:\n")
	b.WriteString("    #   - \"<port-hote-libre>:<port-conteneur>\"\n")
	b.WriteString("    # environment:\n")
	b.WriteString("    #   CLE: \"valeur\"\n")
	b.WriteString("\n")

	b.WriteString("volumes:\n")
	fmt.Fprintf(&b, "  %s:\n", targetVolume)
	b.WriteString("    external: true\n")

	return b.String(), nil
}

// composeName : nom de service compose sur (`[a-zA-Z0-9._-]`).
func composeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-._")
	if out == "" {
		out = "service"
	}
	return out
}

// yamlQuote : scalaire YAML double-quote, seuls `\` et `"` sont a echapper
// (les valeurs rendues ici sont des chemins et des noms, jamais de sauts de
// ligne).
func yamlQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
