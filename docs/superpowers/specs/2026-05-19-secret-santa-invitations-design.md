# Secret Santa — Invitations par liste pré-chargée — Spec de conception

Date : 2026-05-19
Statut : approuvé pour passage au plan d'implémentation

## 1. Contexte

Le type d'événement `secret_santa` (spec `2026-05-18-secret-santa-design.md`)
fonctionne aujourd'hui en **auto-inscription** : chaque participant remplit son
nom et son email sur la page publique, reçoit par email un lien magique, puis
clique ce lien pour atteindre le formulaire des 3 souhaits.

En pratique, l'organisateur dispose **déjà** de la liste des participants (nom,
email, langue) dans une autre plateforme, sous forme d'export CSV. Faire passer
chaque personne par l'auto-inscription est un détour inutile : l'admin peut
pré-charger la liste et envoyer à chacun, directement, un email contenant son
lien personnel vers le formulaire de souhaits.

Cette spec ajoute donc deux actions admin — **import CSV** et **envoi des
invitations** — sans toucher au modèle de données. L'auto-inscription publique
est **conservée** comme filet pour les personnes absentes de la liste.

## 2. Objectifs

- L'admin importe une liste de participants depuis un fichier CSV.
- L'admin déclenche l'envoi d'un email d'invitation à chaque participant : un
  email contenant son lien personnel vers le formulaire de souhaits.
- Un invité qui clique le lien reçu arrive **directement** sur le formulaire des
  3 souhaits (comportement déjà en place — le lien mène à `/santa/edit`).
- L'auto-inscription publique reste inchangée comme chemin de repli.
- Correction du rendu de l'icône enveloppe (trop petite) sur la page de
  confirmation publique.

## 3. Non-objectifs (hors périmètre, volontairement)

- **Intégration directe avec l'autre plateforme** : pas d'API, pas de
  synchronisation. Le fichier CSV est le seul contrat d'échange.
- **Nouvelle table ou colonne** : `santa_participants` et `email_messages`
  couvrent déjà tous les besoins.
- **Stockage des colonnes adresse / téléphone / ville / pays** du CSV : elles
  sont lues puis ignorées.
- **Raccourcir l'auto-inscription publique en redirigeant vers le formulaire** :
  rejeté pour raison de sécurité — voir §4.
- **Renvoi ciblé d'une invitation en échec (bounce)** : le bouton d'envoi a une
  sémantique de rattrapage global, pas de relance individuelle. Cohérent avec le
  bouton « Renvoyer » des emails de révélation. Recours manuel en §8.
- **Re-tirage, exclusions de paires** : déjà hors périmètre de la spec d'origine.

## 4. Sécurité — pourquoi l'auto-inscription publique garde sa confirmation email

L'auto-inscription publique **ne doit pas** être raccourcie en redirigeant
l'utilisateur directement vers le formulaire de souhaits après le POST.

`UpsertSantaParticipant` est idempotent sur le couple `(event_id, email)`. Si
l'on rendait `/santa/edit?token=…` accessible directement après le POST public,
il suffirait de saisir l'email d'une autre personne dans le formulaire public
pour recevoir **son** jeton et lire / modifier sa liste de souhaits.

L'email de confirmation est précisément la barrière : le lien magique n'arrive
que dans la boîte du propriétaire réel de l'adresse. Le flux public reste donc
**nom + email → « consultez votre boîte mail » → clic sur le lien → formulaire**.

La distinction tient en une phrase :

- **Invité (liste pré-chargée)** — l'email envoyé par l'admin *est* la
  confirmation. Recevoir cet email dans sa propre boîte prouve la possession de
  l'adresse ; le clic sur le lien mène directement au formulaire.
- **Auto-inscription publique** — la personne s'annonce elle-même ; la
  confirmation par email reste obligatoire pour empêcher l'usurpation.

Le mécanisme est identique dans les deux cas (un lien à jeton envoyé par email) ;
seule diffère la personne qui déclenche l'envoi (l'admin vs le participant).

## 5. Décisions

| Sujet | Choix | Raison |
|---|---|---|
| Entrée de la liste | Import de fichier CSV | L'organisateur a déjà la liste ailleurs ; le CSV est le format d'export universel. |
| Repérage des colonnes | Par nom d'en-tête | L'ordre des colonnes de l'export peut varier ; le nom est stable. |
| Séparateur CSV | Détection automatique `,` / `;` | Les exports Excel en locale française utilisent `;`. |
| Email d'invitation | Réemploi de l'email à lien magique existant | Même contenu utile (un lien à jeton) ; un seul template à maintenir. |
| Cible de l'envoi | Participants sans email `link` déjà enregistré | Évite le double envoi ; un nouvel import suivi d'un envoi ne touche que les nouveaux. |
| Envoi des invitations | Goroutine de fond, à débit limité | Même contrainte que les emails de révélation : éviter le timeout HTTP. |
| Auto-inscription publique | Conservée, flux inchangé | Filet de sécurité ; la confirmation email reste la barrière anti-usurpation (§4). |
| Modèle de données | Inchangé | `santa_participants` + `email_messages` suffisent. |

## 6. Modèle de données

**Aucun changement.** Ni nouvelle table, ni nouvelle colonne, ni migration.

- L'import crée / met à jour des lignes `santa_participants` via
  `UpsertSantaParticipant`, qui met à jour nom et langue en **préservant** le
  jeton, les souhaits et l'état de complétion d'une fiche existante.
- L'envoi des invitations enregistre une ligne `email_messages` de `kind`
  `link` via `RecordEmailSent` — exactement comme le fait déjà
  l'auto-inscription publique. La contrainte d'unicité `(participant_id, kind)`
  garantit au plus une invitation par participant.

## 7. Import CSV (admin)

### 7.1 Route et handler

- Route : `POST /admin/santa/import`, enregistrée avec `app.requireAdmin(…)`
  dans `main.go`.
- Handler : `handleAdminSantaImport` dans `handlers.go`.
- Le formulaire est en `multipart/form-data` ; le fichier est lu via
  `r.FormFile("file")` après `r.ParseMultipartForm`.
- L'import est **refusé après le tirage** (`santa_drawn_at` non nul) — un
  participant importé après le tirage ne pourrait pas entrer dans le cycle.
  Cohérent avec la suppression de participants.

### 7.2 Analyse du fichier

Parsing avec `encoding/csv` :

1. Le **BOM UTF-8** éventuel en tête de fichier est retiré.
2. Le séparateur est détecté sur la première ligne : si elle contient plus de
   `;` que de `,`, le séparateur est `;`, sinon `,`.
3. La première ligne est l'en-tête. Chaque champ utile est rattaché à une
   colonne par **nom d'en-tête**, comparaison insensible à la casse et aux
   espaces de bordure :
   - email — en-têtes acceptés : `email`, `e-mail`, `courriel`
   - prénom — `prénom`, `prenom`, `first name`, `first_name`
   - nom — `nom`, `last name`, `last_name`
   - langue — `langue`, `lang`, `language`
4. Les autres colonnes (`Adresse`, `Code postal`, `Ville`, `Pays`,
   `Téléphone`, `Portable`…) sont ignorées.
5. Si la colonne `email` est absente, l'import échoue avec un message clair —
   aucune ligne n'est traitée.

### 7.3 Traitement d'une ligne

Pour chaque ligne de données :

- L'email est nettoyé (`TrimSpace`). Une ligne dont l'email est vide ou ne
  contient pas de `@` est **ignorée** et comptée comme « ignorée ».
- La langue est normalisée : une valeur commençant par `en` (casse ignorée) →
  `en` ; tout le reste, y compris vide → `fr` (langue par défaut du projet).
- Le handler vérifie l'existence préalable de la fiche via
  `GetSantaParticipantByEmail` puis appelle `UpsertSantaParticipant(db,
  eventID, firstName, lastName, email, lang)`. La vérification préalable sert
  uniquement à distinguer « créé » de « mis à jour » dans le bilan.

### 7.4 Bilan affiché

Après l'import, la page admin affiche un récapitulatif :
« X participants importés, Y mis à jour, Z lignes ignorées ».

L'import est **idempotent** : ré-importer le même fichier met à jour les fiches
existantes (nom, langue) sans doublon, sans réinitialiser les souhaits déjà
saisis ni invalider les jetons.

## 8. Envoi des invitations (admin)

### 8.1 Route et handler

- Route : `POST /admin/santa/invite`, enregistrée avec `app.requireAdmin(…)`.
- Handler : `handleAdminSantaInvite` dans `handlers.go`.
- Refusé après le tirage (les emails de révélation sont alors déjà partis ;
  envoyer une invitation n'aurait aucun sens).
- Le handler lance l'envoi de fond puis ré-affiche la page admin avec un
  message de confirmation.

### 8.2 Envoyeur de fond `sendInviteEmails`

Nouvelle fonction dans `email.go`, calquée sur `sendRevealEmails` :

- `dispatchInviteEmails(eventID)` — exécute `sendInviteEmails` dans une
  goroutine en production (`AsyncEmail`), de façon synchrone dans les tests.
- `sendInviteEmails(eventID)` :
  1. Garde de concurrence partagée : `app.sending` (clé `eventID`) — un envoi
     d'invitations ne démarre pas si un envoi est déjà en cours pour le même
     événement.
  2. Charge les participants de l'événement et les `email_messages` existants
     (`ListEmailMessages`). Construit l'ensemble des `participant_id` ayant déjà
     une ligne de `kind` `link`.
  3. Pour chaque participant **sans** email `link` enregistré : rend l'email du
     lien magique dans sa langue (`renderSantaLinkEmail`), l'envoie via
     `sendWithRetry` (retry/backoff, 3 tentatives), puis enregistre le résultat
     avec `RecordEmailSent(db, p.ID, "link", messageID, p.Email)`.
  4. Respecte le débit `EmailSendDelay` entre deux envois.

Conséquence de la cible « sans email `link` » : ré-cliquer « Envoyer les
invitations » ne renvoie pas aux personnes déjà invitées ; après un nouvel
import, seules les nouvelles fiches sont contactées. Une invitation en échec
(bounce) n'est pas renvoyée par le bouton — l'admin la voit dans la colonne de
statut. Recours manuel disponible avant le tirage : supprimer puis ré-importer
le participant crée une fiche neuve (nouveau jeton, aucune ligne `link`) que le
prochain envoi rattrapera.

### 8.3 Email d'invitation

Aucun nouveau template. `renderSantaLinkEmail` et `templates/email_santa_link.html`
sont réutilisés tels quels.

La clé i18n `santa_email_link_intro` est reformulée pour qu'elle se lise
naturellement aussi bien pour un invité (qui ne s'est pas inscrit lui-même) que
pour quelqu'un venu de l'auto-inscription — une formulation neutre du type
« Voici votre lien personnel pour composer votre liste de souhaits. » Le titre
de l'événement est déjà disponible dans le template.

## 9. Interface admin (`admin_santa.html`)

**Avant le tirage**, dans le panneau supérieur, deux éléments sont ajoutés :

- un formulaire d'**import CSV** : champ fichier + bouton « Importer une liste
  (CSV) », avec une courte aide indiquant les colonnes attendues
  (`email`, `Nom`, `Prénom`, `Langue`) ;
- un bouton « **Envoyer les invitations** » (POST vers `/admin/santa/invite`),
  avec confirmation JavaScript.

Le bilan de l'import et la confirmation d'envoi s'affichent via les champs
`Success` / `Error` de `PageData` déjà utilisés par la page.

La page indique le **nombre d'invitations envoyées** (nombre de lignes
`email_messages` de `kind` `link`, déjà disponible côté serveur dans la map
`LinkStatus`). La colonne « statut email lien » de la table des participants
existe déjà et montre, par participant, l'état de l'invitation (envoyé /
délivré / bounce…).

**Après le tirage**, ces éléments disparaissent — la page bascule sur le
tirage / la révélation, comme aujourd'hui.

## 10. Correction de l'icône de confirmation publique

Sur `templates/public_santa.html`, la carte de confirmation affichée après
l'auto-inscription contient une icône enveloppe au format glyphe texte
(`&#x2709;`), rendue trop petite et trop fine dans son cercle de 56 px.

Correction : remplacer le glyphe texte par une **icône Font Awesome**
(`<i class="fa-solid fa-envelope">`), comme les autres icônes de la page. Font
Awesome rend l'enveloppe à une taille proportionnée et cohérente. La règle CSS
`.confirmation-icon` de `static/style.css` est conservée ; sa taille de police
est ajustée si nécessaire pour que l'enveloppe occupe correctement le cercle.

Le flux d'auto-inscription publique lui-même n'est **pas** modifié (§4).

## 11. Routes

Nouvelles routes admin :

- `POST /admin/santa/import` → `handleAdminSantaImport`
- `POST /admin/santa/invite` → `handleAdminSantaInvite`

Aucune route publique ajoutée ou modifiée.

## 12. i18n

Nouvelles clés FR/EN dans `i18n.go` :

- import : libellé du bouton « Importer une liste (CSV) », aide sur les colonnes
  attendues, message de bilan « X importés, Y mis à jour, Z ignorées », message
  d'erreur « colonne email introuvable », message d'erreur « fichier illisible ».
- invitations : libellé du bouton « Envoyer les invitations », texte de
  confirmation JavaScript, message de confirmation d'envoi, libellé du compteur
  « invitations envoyées ».

Clé modifiée :

- `santa_email_link_intro` — reformulée pour convenir aux invités comme aux
  auto-inscrits (§8.3).

## 13. Cas limites

- **Fichier sans colonne `email`** → import refusé, message clair, aucune fiche
  touchée.
- **Fichier vide ou illisible** → message d'erreur, aucune fiche touchée.
- **Ligne sans email valide** → ignorée, comptée dans « lignes ignorées ».
- **Ré-import du même fichier** → mises à jour idempotentes, pas de doublon, les
  souhaits déjà saisis sont préservés.
- **Séparateur `;`** (export Excel FR) → détecté automatiquement.
- **BOM UTF-8 en tête de fichier** → retiré avant analyse.
- **Import ou envoi après le tirage** → refusés.
- **Double clic sur « Envoyer les invitations »** → la garde de concurrence
  `app.sending` empêche un second envoi simultané ; et la cible « sans email
  `link` » empêche tout double envoi même en cliquant après la fin du premier.
- **Participant déjà auto-inscrit puis présent dans le CSV** →
  `UpsertSantaParticipant` réutilise sa fiche ; il a déjà un email `link` (créé
  à son auto-inscription) donc l'envoi d'invitations ne le contacte pas une
  seconde fois.
- **Redémarrage du serveur pendant l'envoi** → goroutine interrompue ; les
  participants sans email `link` sont rattrapés au prochain clic sur « Envoyer
  les invitations ».

## 14. Stratégie de tests (écrits avant l'implémentation — TDD)

### Analyse CSV (fonction pure, testable isolément)

L'analyse du CSV est isolée dans une fonction pure (`parseSantaCSV` ou
équivalent) prenant un `io.Reader` et renvoyant les lignes structurées + les
compteurs, sans accès base de données :

- en-têtes dans le désordre → colonnes correctement rattachées ;
- séparateur `;` → détecté ;
- BOM UTF-8 → retiré ;
- en-têtes avec casse / espaces variables → rattachés ;
- colonne `email` absente → erreur ;
- ligne sans `@` dans l'email → comptée comme ignorée ;
- valeurs de `Langue` (`fr`, `Français`, `en`, vide, inconnu) → normalisées.

### Handlers + envoi

- `POST /admin/santa/import` : crée les participants attendus ; un ré-import met
  à jour sans doublon et préserve les souhaits ; refusé après le tirage.
- `sendInviteEmails` : envoie un email `link` à chaque participant non encore
  invité ; n'envoie pas à un participant ayant déjà un email `link` ; enregistre
  une ligne `email_messages` de `kind` `link` ; jeton récupérable via
  `fakeEmailSender`.
- `POST /admin/santa/invite` : déclenche l'envoi ; refusé après le tirage.
- Un invité qui suit le lien de l'email arrive sur `/santa/edit` avec le
  formulaire de souhaits (couvert par les tests existants de `handleSantaEdit`).

## 15. Fichiers créés / modifiés

Créés : aucun. Les tests de l'analyse CSV et des nouveaux handlers sont ajoutés
aux fichiers de test Secret Santa existants (`santa_test.go` pour la fonction
pure d'analyse, `santa_handlers_test.go` pour les handlers et l'envoi).

Modifiés :

- `handlers.go` — `handleAdminSantaImport`, `handleAdminSantaInvite`, et la
  fonction pure d'analyse CSV.
- `email.go` — `dispatchInviteEmails`, `sendInviteEmails`.
- `main.go` — enregistrement des deux nouvelles routes admin.
- `i18n.go` — nouvelles clés, reformulation de `santa_email_link_intro`.
- `templates/admin_santa.html` — formulaire d'import + bouton d'envoi des
  invitations + compteur.
- `templates/public_santa.html` — icône de confirmation Font Awesome.
- `static/style.css` — ajustement éventuel de `.confirmation-icon`.
- `santa_test.go`, `santa_handlers_test.go` — nouveaux tests (voir ci-dessus).

Inchangés : `schema.sql`, `models.go` (aucun changement de modèle de données),
`templates/email_santa_link.html`, le flux public d'auto-inscription.
