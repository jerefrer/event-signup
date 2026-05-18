# Secret Santa — Spec de conception

Date : 2026-05-18
Statut : approuvé pour passage au plan d'implémentation

## 1. Contexte

La plateforme event-signup gère aujourd'hui deux types d'événements : `tasks`
(inscription à des tâches) et `attendance` (RSVP oui/non). Cette spec ajoute un
troisième type, `secret_santa` : un échange de cadeaux où chaque participant
enregistre une liste de 3 souhaits, puis l'admin déclenche un tirage qui assigne
à chaque personne une autre personne à gâter et envoie à chacun un email lui
révélant qui il/elle a tiré, avec les 3 souhaits de cette personne.

L'architecture existante (module Go unique, fichiers plats, SQLite embarqué,
templates `html/template`, i18n FR/EN) est conservée. Aucun code d'envoi d'email
n'existe à ce jour : il est ajouté de zéro.

## 2. Objectifs

- Nouveau type d'événement `secret_santa`.
- Inscription publique en deux temps protégée par un lien magique envoyé par email.
- Chaque participant enregistre 3 souhaits (tous obligatoires).
- L'admin déclenche un tirage (dérangement) et l'envoi des emails de révélation.
- Module d'envoi d'email via AWS SES (SDK Go v2).
- Tests écrits avant l'implémentation (TDD).

## 3. Non-objectifs (hors périmètre, volontairement)

- Export CSV pour les événements `secret_santa` (la table admin affiche déjà tout).
- Exclusions de paires dans le tirage (ex. un couple).
- Re-tirage : le tirage est unique ; pour recommencer il faut recréer l'événement.
- Emails d'invitation : avec l'auto-inscription, l'admin partage lui-même le lien
  public de l'événement. Les seuls emails envoyés par l'app sont le lien magique
  et l'email de révélation.
- Refonte de `handlers.go` (fichier volumineux) : on suit les patterns existants,
  on ne mélange pas refactoring et fonctionnalité.

## 4. Décisions

| Sujet | Choix | Raison |
|---|---|---|
| Envoi d'email | AWS SES via `aws-sdk-go-v2` | Compte AWS déjà disponible ; SDK natif SES. |
| Entrée des participants | Auto-inscription publique | Pas de liste pré-saisie ; l'admin partage le lien. |
| Souhaits | Les 3 obligatoires | Toute fiche complétée a 3 souhaits remplis. |
| Tirage | Dérangement, sans exclusions | Algorithme de Sattolo (cycle unique). |
| Protection de l'édition | Lien magique par email | Le formulaire de souhaits n'est joignable que via le lien envoyé à l'email → impossible de lire/modifier la liste d'un email qu'on ne possède pas. |
| Envoi des emails de révélation | Goroutine de fond, à débit limité | Évite le timeout HTTP ; suivi via `email_sent_at` + bouton « Renvoyer » comme filet de sécurité. |

## 5. Modèle de données

Conformément au pattern du projet : toute nouvelle colonne est ajoutée **à la
fois** dans `schema.sql` (DB neuves) **et** via `migrateColumn()` dans `models.go`
(DB existantes).

### 5.1 Table `events` — colonne ajoutée

- `santa_drawn_at TEXT` — `NULL` tant que le tirage n'a pas eu lieu ; horodatage
  une fois le tirage effectué. Sert de verrou : inscriptions closes et tirage
  non rejouable une fois cette colonne remplie.

### 5.2 Nouvelle table `santa_participants`

| Colonne | Type | Rôle |
|---|---|---|
| `id` | INTEGER PK | identité |
| `event_id` | INTEGER FK → `events(id)` ON DELETE CASCADE | événement |
| `first_name`, `last_name` | TEXT | participant |
| `email` | TEXT | email (unique logiquement par événement, casse ignorée) |
| `lang` | TEXT | langue de l'inscription (`fr`/`en`) → langue des emails reçus |
| `token` | TEXT UNIQUE | jeton aléatoire du lien d'édition personnel |
| `wish_buy` | TEXT | souhait 1 — achetable (< 10 €) |
| `wish_make` | TEXT | souhait 2 — fabriqué ou trouvé |
| `wish_free` | TEXT | souhait 3 — au choix |
| `completed_at` | TEXT NULL | horodatage de complétion des 3 souhaits |
| `assigned_to_id` | INTEGER NULL FK → `santa_participants(id)` | personne tirée |
| `email_sent_at` | TEXT NULL | horodatage d'envoi de l'email de révélation |
| `created_at`, `updated_at` | DATETIME | suivi |

Index : `event_id`, `token`.

**État d'une fiche :**
- *en attente* — `completed_at IS NULL` : email demandé, souhaits pas encore remplis.
- *complétée* — `completed_at IS NOT NULL` : 3 souhaits remplis.

Le tirage n'inclut **que les fiches complétées**.

## 6. Algorithme de tirage

Fonction pure dans `models.go`, isolée et testable :

```
DrawSecretSanta(ids []int64, rng *rand.Rand) (map[int64]int64, error)
```

- Implémente l'**algorithme de Sattolo** : produit une permutation cyclique
  aléatoire unique. Un cycle de longueur ≥ 2 n'a aucun point fixe → personne ne
  se tire soi-même, garanti, sans boucle de retry.
- Retourne une `map giver → receiver`.
- Retourne une erreur si `len(ids) < 2`.
- `rng` est injecté → tests déterministes avec une graine fixe. Le handler crée
  un `rand.Rand` initialisé sur l'heure courante.

Persistance — `SaveSantaDraw(db, eventID, assignments)` : dans une **transaction**,
écrit `assigned_to_id` pour chaque participant et pose `events.santa_drawn_at`.

## 7. Module email (`email.go`, nouveau fichier)

```
type EmailSender interface {
    Send(ctx context.Context, to, subject, htmlBody, textBody string) error
}
```

Implémentations :
- `SESSender` — AWS SES via `aws-sdk-go-v2/service/sesv2`.
- `LogSender` — logge l'email dans la console ; utilisé quand SES n'est pas
  configuré (développement, tests manuels).
- `fakeEmailSender` (fichier de test) — enregistre les emails envoyés pour les
  assertions.

`App` reçoit un champ `Email EmailSender`.

### 7.1 Configuration (`main.go`)

- `EVENT_SIGNUP_EMAIL_FROM` — adresse expéditeur vérifiée. Si vide → `LogSender`.
  Si renseignée → `SESSender`.
- `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` — variables standard
  du SDK AWS, récupérées automatiquement par `config.LoadDefaultConfig`.
- `EVENT_SIGNUP_EMAIL_RATE` — nombre d'emails par seconde, défaut `2`.

### 7.2 Deux emails (templates embarqués, rendus sans le layout)

- `templates/email_santa_link.html` — email du lien magique, envoyé à
  l'inscription. Contient le lien `/santa/edit?token=…`.
- `templates/email_santa_reveal.html` — email de révélation, envoyé après le
  tirage. Contient le nom de la personne tirée + ses 3 souhaits + un lien vers
  l'événement.

Chaque email est rendu dans la langue (`lang`) du participant destinataire.

### 7.3 Envoi des emails de révélation

`sendRevealEmails(eventID)` — fonction synchrone, appelée par le handler dans une
goroutine (`go app.sendRevealEmails(...)`) et directement par les tests :

1. Charge les participants complétés de l'événement avec `email_sent_at IS NULL`.
2. Pour chacun : rend l'email de révélation dans sa langue, l'envoie via
   `app.Email`. En cas de succès, pose `email_sent_at`. En cas d'échec, **retry
   avec backoff** (jusqu'à 3 tentatives) ; si l'échec persiste, laisse
   `email_sent_at` à `NULL`.
3. Respecte le débit `EVENT_SIGNUP_EMAIL_RATE` (pause entre deux envois).

**Garde de concurrence** : `App` détient un garde par événement (ex. `sync.Map`
de `eventID` en cours d'envoi). `sendRevealEmails` ne démarre pas si un envoi est
déjà en cours pour le même événement → pas de double envoi si l'admin clique
« Renvoyer » pendant que le premier envoi tourne.

Le débit prudent par défaut (2/s) reste très en deçà des limites de débit SES en
production ; le retry/backoff couvre une erreur de throttling ou réseau transitoire.

## 8. Flux public (inscription en deux temps)

1. **Page publique** — `GET /e/{slug}` pour un événement `secret_santa` rend
   `public_santa.html` : infos de l'événement + formulaire **nom + email**
   (« Recevez votre lien personnel »). Si le navigateur a un jeton en
   `localStorage`, un bouton « Continuer ma liste » mène directement à l'édition.

2. **`POST /santa/register`** — crée la fiche participant (email, nom, jeton
   aléatoire, langue ; *upsert* par email — re-demander un lien réutilise la
   fiche existante). Envoie l'email du lien magique (envoi **synchrone**, un seul
   destinataire). Affiche « vérifiez votre boîte mail ». Si l'envoi SES échoue,
   l'erreur est affichée immédiatement. Bloqué si le tirage a déjà eu lieu.

3. **`GET /santa/edit?token=…`** — rend `santa_edit.html` : le formulaire des 3
   souhaits, pré-rempli si la fiche a déjà des souhaits. Jeton invalide → page
   d'erreur. Bloqué si le tirage a déjà eu lieu.

4. **`POST /santa/edit`** — valide la présence des **3 souhaits** ; un champ
   manquant → erreur de validation et ré-affichage du formulaire. Enregistre les
   souhaits, pose `completed_at` si c'est la première complétion. La page de
   confirmation stocke le jeton en `localStorage` (JS) pour les retours ultérieurs.
   Bloqué si le tirage a déjà eu lieu.

## 9. Flux admin

- **`GET /admin/event/santa?id=…`** — rend `admin_santa.html` :
  - Compteur : « X inscrits, dont Y ont complété leur liste ».
  - Bouton **« Mélanger et envoyer »** — confirmation JS obligatoire. Désactivé
    si moins de 2 fiches complétées. Si des fiches sont en attente, un
    avertissement indique qu'elles seront exclues du tirage.
  - Bouton **« Révéler »** — bascule JS qui dévoile la table (rendue côté serveur,
    masquée par défaut) : noms, emails, 3 souhaits, et après tirage la personne
    tirée par chacun.
  - Après le tirage : statut « X/Y emails envoyés » + bouton **« Renvoyer »**.
  - Bouton de suppression par participant — **uniquement avant le tirage**.

- **`POST /admin/santa/draw`** :
  1. Vérifie : événement de type `secret_santa`, `santa_drawn_at` est `NULL`.
  2. Charge les participants complétés ; si < 2 → erreur affichée sur la page admin.
  3. `DrawSecretSanta` sur leurs IDs.
  4. `SaveSantaDraw` (transaction) : assignations + `santa_drawn_at`.
  5. Lance `go app.sendRevealEmails(eventID)`.
  6. Redirige vers la page santa avec « Tirage effectué, envoi des emails en cours ».

- **`POST /admin/santa/resend`** — relance `go app.sendRevealEmails(eventID)`
  (ne re-cible que les `email_sent_at IS NULL`). Aucun re-tirage.

- **`POST /admin/santa/participant/delete`** — supprime un participant. **Autorisé
  uniquement avant le tirage** : refusé une fois `santa_drawn_at` rempli, car
  supprimer après le tirage casserait le cycle (le donneur de la personne
  supprimée n'aurait plus de receveur, et inversement) alors que les emails sont
  déjà partis. Avant le tirage il n'existe aucun cycle ; la suppression sert au
  nettoyage des inscriptions en double, d'essai ou erronées, et des désistements.

- **`admin_event_edit.html`** — 3ᵉ option de type `secret_santa` ; pour ce type,
  l'éditeur de groupes/tâches est masqué (comme pour `attendance`).
- **`admin_events.html`** — pour un événement `secret_santa`, lien vers
  `/admin/event/santa`.

## 10. Routes

Publiques : `GET /e/{slug}` (branche `secret_santa`) · `POST /santa/register` ·
`GET /santa/edit` · `POST /santa/edit`

Admin : `GET /admin/event/santa` · `POST /admin/santa/draw` ·
`POST /admin/santa/resend` · `POST /admin/santa/participant/delete`

## 11. Templates

À créer : `public_santa.html` (page d'inscription), `santa_edit.html` (formulaire
des 3 souhaits), `admin_santa.html` (admin), `email_santa_link.html`,
`email_santa_reveal.html`.

À modifier : `admin_event_edit.html` (option de type), `admin_events.html` (lien).

## 12. i18n

Nouvelles clés FR/EN dans `i18n.go`, regroupées par usage :
- Page publique : titre, libellés nom/email, bouton « recevez votre lien »,
  message « vérifiez votre boîte mail », message « inscriptions closes ».
- Formulaire des souhaits : les 3 libellés et leurs sous-titres —
  - souhait 1 : « Quelque chose qui peut être acheté (moins de 10 €) » /
    sous-titre « Pour ceux qui n'ont pas le temps — un stylo, des chaussettes,
    du chocolat… »
  - souhait 2 : « Quelque chose qui peut être fabriqué ou trouvé » / sous-titre
    « Pour ceux qui n'ont pas d'argent — une plante, un plat, un poème, une
    prière… »
  - souhait 3 : « Quelque chose au choix »
  - erreur de validation « les 3 souhaits sont requis », confirmation d'enregistrement.
- Admin : libellés des compteurs, boutons « Mélanger et envoyer », « Révéler »,
  « Renvoyer », texte de confirmation du tirage, avertissement fiches en attente,
  statut d'envoi.
- Emails : objet et corps du lien magique ; objet et corps de la révélation
  (intro, libellés des 3 souhaits, lien vers l'événement).

## 13. Prérequis SES (configuration AWS, hors code)

À vérifier côté AWS **avant la mise en production** — le code ne peut pas y suppléer :

- Le compte SES doit être en **accès production** (hors « sandbox »). En sandbox,
  l'envoi n'est possible que vers des adresses vérifiées (max 200/24 h) → les
  emails vers les participants échoueraient.
- L'adresse ou le domaine **expéditeur** (`EVENT_SIGNUP_EMAIL_FROM`) doit être
  **vérifié** dans SES.
- La **région** SES doit être connue et fournie via `AWS_REGION`.
- Le quota d'envoi /24 h en production (dizaines de milliers) ne contraint pas un
  Secret Santa.

Tant que `EVENT_SIGNUP_EMAIL_FROM` n'est pas défini, l'app utilise `LogSender` :
le développement et les tests fonctionnent sans SES.

## 14. Cas limites

- Tirage avec moins de 2 fiches complétées → erreur affichée, aucun tirage.
- Fiches en attente au moment du tirage → exclues ; avertissement préalable.
- Inscription ou édition après le tirage → pages en lecture seule
  « inscriptions closes ».
- Email en double à `/santa/register` → réutilise la fiche existante (upsert).
- Jeton invalide ou absent à `/santa/edit` → page d'erreur.
- Échec d'envoi partiel → tirage conservé, `email_sent_at` reste `NULL` pour les
  échecs, bouton « Renvoyer » disponible.
- Redémarrage du serveur pendant l'envoi → goroutine interrompue ; les
  `email_sent_at IS NULL` sont rattrapés par « Renvoyer ».
- Double déclenchement d'envoi (draw + resend simultanés) → garde de concurrence
  par événement empêche le double envoi.
- Suppression d'un participant → autorisée uniquement avant le tirage ; refusée
  une fois `santa_drawn_at` rempli.

## 15. Stratégie de tests (écrits avant l'implémentation — TDD)

### `santa_test.go` (logique modèle)
- `DrawSecretSanta` : aucun point fixe ; résultat bijectif (chaque personne tirée
  une seule fois) ; tous les participants couverts ; déterministe avec graine
  fixe ; cas N=2 ; erreur si N<2.
- CRUD `SantaParticipant` : création ; upsert par email (casse ignorée) ;
  récupération par jeton ; liste par événement ; comptage inscrits/complétés.
- `SaveSantaDraw` : `assigned_to_id` et `santa_drawn_at` persistés ;
  comportement transactionnel.

### `santa_handlers_test.go` (handlers + email)
- `GET /e/{slug}` rend la page santa pour un événement `secret_santa`.
- `POST /santa/register` : crée une fiche en attente et envoie un email de lien
  (jeton récupéré via `fakeEmailSender`).
- `GET /santa/edit` : jeton valide → formulaire ; jeton invalide → erreur.
- `POST /santa/edit` : enregistre les 3 souhaits ; un souhait manquant → erreur
  de validation ; `completed_at` posé à la première complétion.
- `register` et `edit` bloqués après le tirage.
- `POST /admin/santa/draw` : n'inclut que les fiches complétées ; < 2 complétées
  → erreur ; assignations et `santa_drawn_at` écrits ; 2ᵉ tirage refusé.
- `sendRevealEmails` : un email par participant complété, nommant la bonne
  personne tirée et contenant ses 3 souhaits ; `email_sent_at` posé après succès.
- `POST /admin/santa/resend` : ne re-cible que les `email_sent_at IS NULL`.
- `POST /admin/santa/participant/delete` : supprime un participant avant le
  tirage ; refusé une fois `santa_drawn_at` rempli.

`testutil_test.go` : l'`App` de test reçoit le `fakeEmailSender`.

## 16. Fichiers créés / modifiés

Créés : `email.go`, `templates/public_santa.html`, `templates/santa_edit.html`,
`templates/admin_santa.html`, `templates/email_santa_link.html`,
`templates/email_santa_reveal.html`, `santa_test.go`, `santa_handlers_test.go`.

Modifiés : `schema.sql`, `models.go` (struct + CRUD + migrations + tirage),
`handlers.go` (handlers publics et admin), `main.go` (routes, `EmailSender`,
config), `i18n.go`, `testutil_test.go`, `templates/admin_event_edit.html`,
`templates/admin_events.html`, `static/admin.js` (confirmation tirage, bascule
« Révéler »), `go.mod` / `go.sum` (dépendances `aws-sdk-go-v2`).
